// Package gossa is M1's go/ssa frontend: it lowers golang.org/x/tools/go/ssa
// into ir.Prog over the GoSSA dialect.
//
// Stage A subset: integers of every width, bools, read-only slices of
// integers, calls between modelled functions in the same package. Phi nodes
// become block parameters; `&xs[i]` followed by `*` becomes a `load`;
// anything else that produces a value is havoc (function marked partial) or
// rejects the function.
package gossa

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/ir"
)

const Frontend = "gossa"

var specRe = regexp.MustCompile(`go2lean:spec\s+([\w.]+)`)

// Translate loads the single package matching pattern (relative to dir) and
// lowers every top-level function. It returns the program and the Go module
// path.
func Translate(dir, pattern string) (*ir.Prog, string, error) {
	progs, modPath, err := TranslateAll(dir, pattern)
	if err != nil {
		return nil, "", err
	}
	if len(progs) != 1 {
		return nil, "", fmt.Errorf("pattern %s matched %d packages; expected exactly one", pattern, len(progs))
	}
	return progs[0], modPath, nil
}

// TranslateAll loads every package matching the patterns in one go (one
// type-check of the dependency graph, which dominates the cost on real
// modules) and lowers each. Packages that fail to load are skipped with
// their errors reported through the returned map, so one broken package
// does not stop a sweep.
func TranslateAll(dir string, patterns ...string) ([]*ir.Prog, string, error) {
	// Tests: true also loads the package's internal test variant
	// ("path [path.test]"), which includes *_test.go files of the same
	// package: that is where agent-written spec-in-Go functions live.
	cfg := &packages.Config{Mode: packages.LoadAllSyntax | packages.NeedModule, Dir: dir, Tests: true}
	loaded, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, "", err
	}
	if errs := loadErrors(loaded); len(errs) > 0 {
		return nil, "", fmt.Errorf("packages %s do not load:\n  %s", strings.Join(patterns, " "), strings.Join(errs, "\n  "))
	}
	pkgs := selectVariant(loaded)
	if len(pkgs) == 0 {
		return nil, "", fmt.Errorf("patterns %s matched no packages", strings.Join(patterns, " "))
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	sprog, spkgs := ssautil.Packages(pkgs, ssa.InstantiateGenerics)
	sprog.Build()

	modPath := ""
	if pkgs[0].Module != nil {
		modPath = pkgs[0].Module.Path
	}
	var progs []*ir.Prog
	for i, spkg := range spkgs {
		if spkg == nil {
			continue
		}
		p, err := translatePackage(pkgs[i].PkgPath, spkg)
		if err != nil {
			return nil, "", err
		}
		progs = append(progs, p)
	}
	return progs, modPath, nil
}

func translatePackage(pkgPath string, spkg *ssa.Package) (*ir.Prog, error) {
	p := &ir.Prog{Package: pkgPath, Frontend: Frontend}
	st := &progState{prog: p, ids: map[*ssa.Function]int{}}

	// Pass 1: pick candidates (package-level functions and the methods of
	// the package's named types) and assign FuncIds so calls can be resolved.
	var fns []*ssa.Function
	seen := map[*ssa.Function]bool{}
	add := func(f *ssa.Function) {
		if f != nil && !seen[f] && f.Synthetic == "" && !isInit(f.Name()) && !isTestFunc(f.Name()) {
			seen[f] = true
			fns = append(fns, f)
		}
	}
	for _, m := range spkg.Members {
		switch m := m.(type) {
		case *ssa.Function:
			add(m)
		case *ssa.Type:
			for _, typ := range []types.Type{m.Type(), types.NewPointer(m.Type())} {
				ms := spkg.Prog.MethodSets.MethodSet(typ)
				for i := 0; i < ms.Len(); i++ {
					add(spkg.Prog.MethodValue(ms.At(i)))
				}
			}
		}
	}
	sort.Slice(fns, func(i, j int) bool { return funcName(fns[i]) < funcName(fns[j]) })
	byName := map[string]*ssa.Function{}
	for _, f := range fns {
		st.ids[f] = len(p.Funcs)
		p.Funcs = append(p.Funcs, &ir.Func{Name: funcName(f), GoSig: goSig(f)})
		byName[funcName(f)] = f
	}
	st.specFor = func(f *ssa.Function) string { return specTarget(f, byName) }
	// Pass 2: bodies.
	for _, f := range fns {
		out := p.Funcs[st.ids[f]]
		t := &fnState{prog: st, f: f, out: out, vars: map[ssa.Value]ir.Var{}, blockID: map[*ssa.BasicBlock]int{}}
		if err := t.translate(); err != nil {
			rejectStub(out, err)
		}
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("internal error: generated program is malformed: %w", err)
	}
	return p, nil
}

// goSig renders a signature with its receiver, as the agent should read it.
func goSig(f *ssa.Function) string {
	if recv := f.Signature.Recv(); recv != nil {
		return "func (" + recv.Name() + " " + recv.Type().String() + ") " + f.Name() + strings.TrimPrefix(f.Signature.String(), "func")
	}
	return f.Signature.String()
}

// funcName is the model name of a function: its Go name, or Type.Method
// for a method (the same for value and pointer receivers).
func funcName(f *ssa.Function) string {
	if recv := f.Signature.Recv(); recv != nil {
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := t.(*types.Named); ok {
			return n.Obj().Name() + "." + f.Name()
		}
	}
	return f.Name()
}

// loadErrors collects the first few type/load errors of the packages (and
// their dependencies), so a broken module graph is reported with its cause
// rather than as "has errors".
func loadErrors(loaded []*packages.Package) []string {
	var errs []string
	seen := map[string]bool{}
	packages.Visit(loaded, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			msg := e.Error()
			if seen[msg] || len(errs) >= 8 {
				continue
			}
			seen[msg] = true
			errs = append(errs, msg)
		}
	})
	return errs
}

// selectVariant keeps the internal test variant of each package when one was
// loaded, else the plain package; external test packages and test mains are
// dropped.
func selectVariant(loaded []*packages.Package) []*packages.Package {
	best := map[string]*packages.Package{}
	for _, p := range loaded {
		if strings.HasSuffix(p.PkgPath, "_test") || strings.HasSuffix(p.PkgPath, ".test") {
			continue
		}
		isTestVariant := strings.HasSuffix(p.ID, ".test]")
		if cur, ok := best[p.PkgPath]; !ok || (isTestVariant && !strings.HasSuffix(cur.ID, ".test]")) {
			best[p.PkgPath] = p
		}
	}
	var out []*packages.Package
	for _, p := range best {
		out = append(out, p)
	}
	return out
}

// isInit matches the package initialiser and the per-file init#N functions
// go/ssa synthesises for `func init()` bodies.
func isInit(name string) bool {
	return name == "init" || strings.HasPrefix(name, "init#")
}

func isTestFunc(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Fuzz") || strings.HasPrefix(name, "Example")
}

// progState is per-program translation state.
type progState struct {
	prog    *ir.Prog
	ids     map[*ssa.Function]int
	specFor func(*ssa.Function) string
}

func (s *progState) newHavoc(width int, site, reason, goHint string) int {
	idx := len(s.prog.HavocWidths)
	s.prog.HavocWidths = append(s.prog.HavocWidths, width)
	s.prog.Havoc = append(s.prog.Havoc, contract.ABIHavoc{Idx: idx, Site: site, Reason: reason, GoHint: goHint})
	return idx
}

// rejectStub turns a function into a body that gets stuck, so callers still
// reference a valid FuncId.
func rejectStub(out *ir.Func, err error) {
	out.Status = contract.ModelRejected
	out.Issues = append(out.Issues, err.Error())
	out.Params = nil
	out.NVals = 0
	out.Entry = 0
	out.Blocks = []ir.Block{{Name: "rejected", Term: ir.Term{Kind: ir.TermPanic, Panic: contract.PanicOther}}}
}

type fnState struct {
	prog    *progState
	f       *ssa.Function
	out     *ir.Func
	vars    map[ssa.Value]ir.Var
	nvals   int
	blockID map[*ssa.BasicBlock]int
	cur     *ir.Block
	partial bool
}

func (t *fnState) fresh() ir.Var {
	v := ir.Var(t.nvals)
	t.nvals++
	return v
}

func (t *fnState) issue(format string, args ...any) {
	t.partial = true
	t.out.Issues = append(t.out.Issues, fmt.Sprintf(format, args...))
}

func (t *fnState) emit(in ir.Instr) { t.cur.Instrs = append(t.cur.Instrs, in) }

func (t *fnState) translate() error {
	f := t.f
	abi, err := abiOf(f)
	if err != nil {
		return err
	}
	t.out.ABI = *abi
	t.out.SpecFor = t.prog.specFor(f)
	if f.Blocks == nil {
		return fmt.Errorf("no body")
	}
	for _, p := range f.Params {
		v := t.fresh()
		t.vars[p] = v
		t.out.Params = append(t.out.Params, v)
	}
	t.out.Blocks = make([]ir.Block, len(f.Blocks))
	for i, b := range f.Blocks {
		t.blockID[b] = i
		t.out.Blocks[i].Name = b.Comment
		for _, in := range b.Instrs {
			if phi, ok := in.(*ssa.Phi); ok {
				v := t.fresh()
				t.vars[phi] = v
				t.out.Blocks[i].Params = append(t.out.Blocks[i].Params, v)
			}
		}
	}
	for i, b := range f.Blocks {
		if err := t.block(i, b); err != nil {
			return fmt.Errorf("block %d (%s): %w", i, b.Comment, err)
		}
	}
	t.out.NVals = t.nvals
	t.out.Entry = 0
	if t.partial {
		t.out.Status = contract.ModelPartial
	} else {
		t.out.Status = contract.ModelComplete
	}
	return nil
}

// phiArgs are the values block `to` receives when entered from `from`.
func (t *fnState) phiArgs(from, to *ssa.BasicBlock) ([]ir.Var, error) {
	idx := -1
	for i, p := range to.Preds {
		if p == from {
			idx = i
		}
	}
	var args []ir.Var
	for _, in := range to.Instrs {
		phi, ok := in.(*ssa.Phi)
		if !ok {
			continue
		}
		if idx < 0 {
			return nil, fmt.Errorf("%s is not a predecessor of %s", from.Comment, to.Comment)
		}
		v, err := t.val(phi.Edges[idx])
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	return args, nil
}

// val returns the variable holding x, materialising constants.
func (t *fnState) val(x ssa.Value) (ir.Var, error) {
	if v, ok := t.vars[x]; ok {
		return v, nil
	}
	c, ok := x.(*ssa.Const)
	if !ok {
		return 0, fmt.Errorf("unsupported value %s (%T)", x.Name(), x)
	}
	cv, err := constVal(c)
	if err != nil {
		return 0, err
	}
	if iv, ok := cv.(ir.IntVal); ok && !slices.Contains(t.out.Consts, iv.Bits) {
		t.out.Consts = append(t.out.Consts, iv.Bits)
	}
	v := t.fresh()
	t.emit(ir.Instr{Dst: v, Op: ir.Const{V: cv}})
	return v, nil
}

func constVal(c *ssa.Const) (ir.Val, error) {
	if c.Value == nil {
		switch {
		case isError(c.Type()):
			return ir.ErrVal{NonNil: false}, nil
		case isPointer(c.Type()):
			return ir.NilPtr{}, nil
		case isSlice(c.Type()):
			return ir.NilSlice{}, nil
		}
		return nil, fmt.Errorf("nil constant of type %s", c.Type())
	}
	if isBool(c.Type()) {
		return ir.BoolVal(constant.BoolVal(c.Value)), nil
	}
	w, signed, ok := intWidth(c.Type())
	if !ok {
		return nil, fmt.Errorf("constant of unsupported type %s", c.Type())
	}
	var bits uint64
	if signed {
		i, _ := constant.Int64Val(c.Value)
		bits = uint64(i)
	} else {
		bits, _ = constant.Uint64Val(c.Value)
	}
	if w < 64 {
		bits &= (1 << uint(w)) - 1
	}
	return ir.IntVal{Width: w, Bits: fmt.Sprint(bits)}, nil
}

func (t *fnState) block(i int, b *ssa.BasicBlock) error {
	t.cur = &t.out.Blocks[i]
	for _, in := range b.Instrs {
		if err := t.instr(b, in); err != nil {
			return fmt.Errorf("%s: %w", in, err)
		}
	}
	return nil
}

func (t *fnState) binop(x *ssa.BinOp) error {
	w, signed, isInt := intWidth(x.X.Type())
	_, countSigned, _ := intWidth(x.Y.Type())
	_ = w
	var op ir.Op
	switch x.Op {
	case token.ADD:
		op = ir.Simple("add")
	case token.SUB:
		op = ir.Simple("sub")
	case token.MUL:
		op = ir.Simple("mul")
	case token.QUO:
		op = pick(signed, "sdiv", "udiv")
	case token.REM:
		op = pick(signed, "srem", "urem")
	case token.AND:
		op = ir.Simple("and")
	case token.OR:
		op = ir.Simple("or")
	case token.XOR:
		op = ir.Simple("xor")
	case token.AND_NOT:
		op = ir.Simple("andNot")
	case token.SHL:
		op = ir.Shl{CountSigned: countSigned}
	case token.SHR:
		op = ir.Shr{Signed: signed, CountSigned: countSigned}
	case token.EQL:
		op = ir.Simple("eq")
	case token.NEQ:
		op = ir.Simple("ne")
	case token.LSS:
		op = pick(signed, "slt", "ult")
	case token.LEQ:
		op = pick(signed, "sle", "ule")
	case token.GTR:
		op = pick(signed, "sgt", "ugt")
	case token.GEQ:
		op = pick(signed, "sge", "uge")
	default:
		return fmt.Errorf("unsupported binary operator %s", x.Op)
	}
	eqOp := x.Op == token.EQL || x.Op == token.NEQ
	if !isInt && !(eqOp && (isBool(x.X.Type()) || isError(x.X.Type()) || isPointer(x.X.Type()))) {
		return fmt.Errorf("binary operator on unsupported type %s", x.X.Type())
	}
	a, err := t.val(x.X)
	if err != nil {
		return err
	}
	c, err := t.val(x.Y)
	if err != nil {
		return err
	}
	t.emit(ir.Instr{Dst: t.def(x), Op: op, Args: []ir.Var{a, c}})
	return nil
}

func pick(signed bool, s, u string) ir.Op {
	if signed {
		return ir.Simple(s)
	}
	return ir.Simple(u)
}

func (t *fnState) def(x ssa.Value) ir.Var {
	v := t.fresh()
	t.vars[x] = v
	return v
}

// havocKind maps a type to the havoc site encoding (see ir.HavocError).
func havocKind(t types.Type) (int, bool) {
	switch {
	case isError(t):
		return ir.HavocError, true
	case isBool(t):
		return ir.HavocBool, true
	}
	w, _, ok := intWidth(t)
	return w, ok
}

func (t *fnState) havoc(x ssa.Value, site, reason, goHint string) error {
	if tu, ok := x.Type().(*types.Tuple); ok {
		// A multi-result call: havoc each component and pack them.
		var parts []ir.Var
		for i := 0; i < tu.Len(); i++ {
			k, ok := havocKind(tu.At(i).Type())
			if !ok {
				return fmt.Errorf("cannot havoc result %d of type %s", i, tu.At(i).Type())
			}
			idx := t.prog.newHavoc(k, fmt.Sprintf("%s: %s #%d", t.f.Name(), site, i), reason, goHint)
			v := t.fresh()
			t.emit(ir.Instr{Dst: v, HavocSite: idx})
			parts = append(parts, v)
		}
		t.issue("%s: %s is havoc (%s)", site, x.Name(), reason)
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("mkTuple"), Args: parts})
		return nil
	}
	k, ok := havocKind(x.Type())
	if !ok {
		return fmt.Errorf("cannot havoc a value of type %s", x.Type())
	}
	idx := t.prog.newHavoc(k, t.f.Name()+": "+site, reason, goHint)
	t.issue("%s: %s is havoc (%s)", site, x.Name(), reason)
	t.emit(ir.Instr{Dst: t.def(x), HavocSite: idx})
	return nil
}

func (t *fnState) instr(b *ssa.BasicBlock, in ssa.Instruction) error {
	switch x := in.(type) {
	case *ssa.Phi, *ssa.DebugRef:
		return nil
	case *ssa.BinOp:
		return t.binop(x)
	case *ssa.UnOp:
		return t.unop(x)
	case *ssa.Convert:
		return t.conv(x, x.X)
	case *ssa.ChangeType:
		return t.conv(x, x.X)
	case *ssa.IndexAddr:
		if !isSlice(x.X.Type()) {
			return fmt.Errorf("index into non-slice type %s", x.X.Type())
		}
		s, err := t.val(x.X)
		if err != nil {
			return err
		}
		i, err := t.val(x.Index)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("indexAddr"), Args: []ir.Var{s, i}})
		return nil
	case *ssa.Alloc:
		zero, ok := zeroVal(x.Type().(*types.Pointer).Elem())
		if !ok {
			return fmt.Errorf("alloc of unsupported type %s", x.Type().(*types.Pointer).Elem())
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Alloc{Zero: zero}})
		return nil
	case *ssa.FieldAddr:
		p, err := t.val(x.X)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.FieldAddr{I: x.Field}, Args: []ir.Var{p}})
		return nil
	case *ssa.Field:
		s, err := t.val(x.X)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Field{I: x.Field}, Args: []ir.Var{s}})
		return nil
	case *ssa.MakeSlice:
		zero, ok := zeroVal(x.Type().Underlying().(*types.Slice).Elem())
		if !ok {
			return fmt.Errorf("make of unsupported slice type %s", x.Type())
		}
		l, err := t.val(x.Len)
		if err != nil {
			return err
		}
		c, err := t.val(x.Cap)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.MakeSlice{Zero: zero}, Args: []ir.Var{l, c}})
		return nil
	case *ssa.Slice:
		return t.slice(x)
	case *ssa.Extract:
		tup, err := t.val(x.Tuple)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Extract{I: x.Index}, Args: []ir.Var{tup}})
		return nil
	case *ssa.Store:
		if _, isGlobal := x.Addr.(*ssa.Global); isGlobal {
			t.issue("store to global %s ignored", x.Addr.Name())
			return nil
		}
		p, err := t.val(x.Addr)
		if err != nil {
			return err
		}
		v, err := t.val(x.Val)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.fresh(), Op: ir.Simple("storePath"), Args: []ir.Var{p, v}})
		return nil
	case *ssa.Call:
		return t.call(x)
	case *ssa.Return:
		switch len(x.Results) {
		case 0:
			v := t.fresh()
			t.emit(ir.Instr{Dst: v, Op: ir.Const{V: ir.UnitVal{}}})
			t.cur.Term = ir.Term{Kind: ir.TermRet, V: v}
		case 1:
			v, err := t.val(x.Results[0])
			if err != nil {
				return err
			}
			t.cur.Term = ir.Term{Kind: ir.TermRet, V: v}
		default:
			var parts []ir.Var
			for _, r := range x.Results {
				v, err := t.val(r)
				if err != nil {
					return err
				}
				parts = append(parts, v)
			}
			tup := t.fresh()
			t.emit(ir.Instr{Dst: tup, Op: ir.Simple("mkTuple"), Args: parts})
			t.cur.Term = ir.Term{Kind: ir.TermRet, V: tup}
		}
		return nil
	case *ssa.Jump:
		args, err := t.phiArgs(b, b.Succs[0])
		if err != nil {
			return err
		}
		t.cur.Term = ir.Term{Kind: ir.TermJump, B: t.blockID[b.Succs[0]], Args: args}
		return nil
	case *ssa.If:
		c, err := t.val(x.Cond)
		if err != nil {
			return err
		}
		targs, err := t.phiArgs(b, b.Succs[0])
		if err != nil {
			return err
		}
		fargs, err := t.phiArgs(b, b.Succs[1])
		if err != nil {
			return err
		}
		t.cur.Term = ir.Term{Kind: ir.TermBr, Cond: c, T: t.blockID[b.Succs[0]], TArgs: targs, F: t.blockID[b.Succs[1]], FArgs: fargs}
		return nil
	case *ssa.Panic:
		t.cur.Term = ir.Term{Kind: ir.TermPanic, Panic: contract.PanicExplicit}
		return nil
	default:
		return fmt.Errorf("unsupported instruction %T", in)
	}
}

func (t *fnState) unop(x *ssa.UnOp) error {
	switch x.Op {
	case token.MUL: // load through a pointer
		if _, isGlobal := x.X.(*ssa.Global); isGlobal {
			return t.havoc(x, "load "+x.X.Name(), "unmodeled-load", "")
		}
		p, err := t.val(x.X)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("loadPath"), Args: []ir.Var{p}})
		return nil
	case token.SUB, token.XOR:
		if _, _, ok := intWidth(x.X.Type()); !ok {
			return fmt.Errorf("unary %s on %s", x.Op, x.X.Type())
		}
		a, err := t.val(x.X)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: pick(x.Op == token.SUB, "neg", "bnot"), Args: []ir.Var{a}})
		return nil
	case token.NOT:
		a, err := t.val(x.X)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("not"), Args: []ir.Var{a}})
		return nil
	}
	return fmt.Errorf("unsupported unary operator %s", x.Op)
}

func (t *fnState) conv(dst ssa.Value, src ssa.Value) error {
	w, _, ok := intWidth(dst.Type())
	_, srcSigned, ok2 := intWidth(src.Type())
	if !ok || !ok2 {
		return fmt.Errorf("conversion %s -> %s not supported", src.Type(), dst.Type())
	}
	a, err := t.val(src)
	if err != nil {
		return err
	}
	t.emit(ir.Instr{Dst: t.def(dst), Op: ir.Conv{Width: w, SignExtend: srcSigned}, Args: []ir.Var{a}})
	return nil
}

func (t *fnState) slice(x *ssa.Slice) error {
	if _, _, ok := sliceElem(x.X.Type()); !ok {
		return fmt.Errorf("reslice of non-slice type %s", x.X.Type())
	}
	if x.Max != nil {
		return fmt.Errorf("three-index slice not supported")
	}
	s, err := t.val(x.X)
	if err != nil {
		return err
	}
	var lo, hi ir.Var
	if x.Low != nil {
		if lo, err = t.val(x.Low); err != nil {
			return err
		}
	} else {
		lo = t.fresh()
		t.emit(ir.Instr{Dst: lo, Op: ir.Const{V: ir.IntVal{Width: 64, Bits: "0"}}})
	}
	if x.High != nil {
		if hi, err = t.val(x.High); err != nil {
			return err
		}
	} else {
		hi = t.fresh()
		t.emit(ir.Instr{Dst: hi, Op: ir.Simple("len"), Args: []ir.Var{s}})
	}
	t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("reslice"), Args: []ir.Var{s, lo, hi}})
	return nil
}

func (t *fnState) call(x *ssa.Call) error {
	hasResult := !isUnitTuple(x.Type())
	if bi, ok := x.Call.Value.(*ssa.Builtin); ok {
		switch bi.Name() {
		case "len", "cap":
			if _, _, ok := sliceElem(x.Call.Args[0].Type()); ok {
				a, err := t.val(x.Call.Args[0])
				if err != nil {
					return err
				}
				t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple(bi.Name()), Args: []ir.Var{a}})
				return nil
			}
		}
		if hasResult {
			return t.havoc(x, "builtin "+bi.Name(), "unmodeled-call", "")
		}
		t.issue("call to builtin %s ignored", bi.Name())
		return nil
	}
	callee := x.Call.StaticCallee()
	if id, ok := t.prog.ids[callee]; ok && callee != nil && hasResult {
		var args []ir.Var
		for _, a := range x.Call.Args {
			v, err := t.val(a)
			if err != nil {
				return err
			}
			args = append(args, v)
		}
		dst := t.def(x)
		next := len(t.out.Blocks)
		// Terminate the current block before appending: append may move the
		// backing array and t.cur would then point at a stale copy.
		t.cur.Term = ir.Term{Kind: ir.TermCall, Callee: id, CArgs: args, Dst: dst, Next: next}
		t.out.Blocks = append(t.out.Blocks, ir.Block{Name: t.cur.Name + ".after-call"})
		t.cur = &t.out.Blocks[next]
		return nil
	}
	name := x.Call.Value.Name()
	if callee != nil {
		name = callee.String()
	}
	if hasResult {
		return t.havoc(x, "call "+name, "unmodeled-call", "")
	}
	t.issue("call to %s ignored (no result, effects not modelled)", name)
	return nil
}

// ---- types -----------------------------------------------------------------

func intWidth(t types.Type) (w int, signed bool, ok bool) {
	b, isBasic := t.Underlying().(*types.Basic)
	if !isBasic {
		return 0, false, false
	}
	switch b.Kind() {
	case types.Int, types.Int64, types.UntypedInt:
		return 64, true, true
	case types.Uint, types.Uint64, types.Uintptr:
		return 64, false, true
	case types.Int32, types.UntypedRune:
		return 32, true, true
	case types.Uint32:
		return 32, false, true
	case types.Int16:
		return 16, true, true
	case types.Uint16:
		return 16, false, true
	case types.Int8:
		return 8, true, true
	case types.Uint8:
		return 8, false, true
	}
	return 0, false, false
}

func isBool(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsBoolean != 0
}

var errorType = types.Universe.Lookup("error").Type()

func isError(t types.Type) bool { return types.Identical(t, errorType) }

func isPointer(t types.Type) bool {
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

func isSlice(t types.Type) bool {
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

// valueOK says whether values of type t are representable in the model:
// integers, bools, errors, pointers to and structs of such values, and
// slices of integers.
func valueOK(t types.Type) bool {
	if _, _, ok := intWidth(t); ok || isBool(t) || isError(t) {
		return true
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return valueOK(u.Elem())
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if !valueOK(u.Field(i).Type()) {
				return false
			}
		}
		return true
	case *types.Slice:
		_, _, ok := intWidth(u.Elem())
		return ok
	}
	return false
}

// zeroVal is the model's zero value of t.
func zeroVal(t types.Type) (ir.Val, bool) {
	if w, _, ok := intWidth(t); ok {
		return ir.IntVal{Width: w, Bits: "0"}, true
	}
	if isBool(t) {
		return ir.BoolVal(false), true
	}
	if isError(t) {
		return ir.ErrVal{}, true
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return ir.NilPtr{}, true
	case *types.Slice:
		return ir.NilSlice{}, true
	case *types.Struct:
		var fields []ir.Val
		for i := 0; i < u.NumFields(); i++ {
			z, ok := zeroVal(u.Field(i).Type())
			if !ok {
				return nil, false
			}
			fields = append(fields, z)
		}
		return ir.StructVal{Fields: fields}, true
	}
	return nil, false
}

// resultOK says whether a result type can be returned by a modelled function.
func resultOK(t types.Type) bool { return valueOK(t) }

func sliceElem(t types.Type) (w int, signed bool, ok bool) {
	s, isSlice := t.Underlying().(*types.Slice)
	if !isSlice {
		return 0, false, false
	}
	return intWidth(s.Elem())
}

func isUnitTuple(t types.Type) bool {
	tu, ok := t.(*types.Tuple)
	return ok && tu.Len() == 0
}

// spell renders a type as source in package pkg would: unqualified for
// pkg's own types, `pkgname.T` otherwise. It also returns the import path
// of a named type from another package.
func spell(pkg *types.Package, t types.Type) (src, importPath string) {
	qual := func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		return p.Name()
	}
	src = types.TypeString(t, qual)
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg() != pkg {
		importPath = n.Obj().Pkg().Path()
	}
	return src, importPath
}

func abiOf(f *ssa.Function) (*contract.ABI, error) {
	abi := &contract.ABI{GoFunc: f.String(), GoSig: f.Signature.String(), Frontend: Frontend}
	if f.Signature.Recv() != nil {
		abi.Method = f.Name()
	}
	pkg := f.Pkg.Pkg
	for _, p := range f.Params {
		ap, err := abiParam(pkg, p.Name(), p.Type())
		if err != nil {
			return nil, fmt.Errorf("parameter %s has unsupported type %s", p.Name(), p.Type())
		}
		abi.Params = append(abi.Params, ap)
	}
	res := f.Signature.Results()
	for i := 0; i < res.Len(); i++ {
		rt := res.At(i).Type()
		if !resultOK(rt) {
			return nil, fmt.Errorf("result type %s not supported", rt)
		}
		var r contract.ABIResult
		r.GoType, r.TypePkg = spell(pkg, rt)
		abi.Results = append(abi.Results, r)
	}
	return abi, nil
}

// abiParam describes one value of type t named name, recursing into
// pointers and structs.
func abiParam(pkg *types.Package, name string, t types.Type) (contract.ABIParam, error) {
	src, ipath := spell(pkg, t)
	switch {
	case isBool(t):
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIBool}, nil
	case isError(t):
		return contract.ABIParam{Go: name, GoType: "error", Model: []string{name}, Kind: contract.ABIError}, nil
	}
	if w, signed, ok := intWidth(t); ok {
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Width: w, Signed: signed, Model: []string{name}, Kind: contract.ABIInt}, nil
	}
	switch u := t.Underlying().(type) {
	case *types.Slice:
		ew, esigned, ok := intWidth(u.Elem())
		if !ok {
			return contract.ABIParam{}, fmt.Errorf("slice of %s", u.Elem())
		}
		esrc, epath := spell(pkg, u.Elem())
		return contract.ABIParam{
			Go: name, GoType: src, TypePkg: ipath, Kind: contract.ABISlice,
			Model: []string{name + "_ref", name + "_off", name + "_len", name + "_cap"},
			Elem:  esrc, ElemPkg: epath, ElemWidth: ew, ElemSigned: esigned,
		}, nil
	case *types.Struct:
		ap := contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIStruct}
		for i := 0; i < u.NumFields(); i++ {
			f, err := abiParam(pkg, u.Field(i).Name(), u.Field(i).Type())
			if err != nil {
				return contract.ABIParam{}, err
			}
			ap.Fields = append(ap.Fields, f)
		}
		return ap, nil
	case *types.Pointer:
		pointee, err := abiParam(pkg, name, u.Elem())
		if err != nil {
			return contract.ABIParam{}, err
		}
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIPtr, Pointee: &pointee}, nil
	}
	return contract.ABIParam{}, fmt.Errorf("unsupported type %s", t)
}

// specTarget names the function f is a spec for. A `go2lean:spec X` doc
// comment wins; otherwise a function named Spec<X>... whose parameters are
// X's parameters followed by X's result is taken as a spec for X, so a
// misplaced marker does not turn a spec into a target.
func specTarget(f *ssa.Function, byName map[string]*ssa.Function) string {
	if decl, ok := f.Syntax().(*ast.FuncDecl); ok && decl.Doc != nil {
		if m := specRe.FindStringSubmatch(decl.Doc.Text()); m != nil {
			return m[1]
		}
	}
	name := f.Name()
	if !strings.HasPrefix(name, "Spec") || f.Signature.Results().Len() != 1 || !isBool(f.Signature.Results().At(0).Type()) {
		return ""
	}
	rest := name[len("Spec"):]
	// Longest target name that is a prefix of the rest and whose signature
	// fits; a method Type.Method matches as TypeMethod.
	best, bestFlat := "", ""
	for tname, target := range byName {
		flat := strings.ReplaceAll(tname, ".", "")
		if target == f || !strings.HasPrefix(rest, flat) || len(flat) <= len(bestFlat) {
			continue
		}
		if specSignatureFits(f.Signature, target.Signature) {
			best, bestFlat = tname, flat
		}
	}
	return best
}

// specSignatureFits checks spec(receiver?, params of target..., results of
// target...) bool.
func specSignatureFits(spec, target *types.Signature) bool {
	var want []types.Type
	if recv := target.Recv(); recv != nil {
		want = append(want, recv.Type())
	}
	for i := 0; i < target.Params().Len(); i++ {
		want = append(want, target.Params().At(i).Type())
	}
	for i := 0; i < target.Results().Len(); i++ {
		want = append(want, target.Results().At(i).Type())
	}
	sp := spec.Params()
	if spec.Results().Len() != 1 || sp.Len() != len(want) {
		return false
	}
	for i, t := range want {
		if !types.Identical(sp.At(i).Type(), t) {
			return false
		}
	}
	return true
}

// GoName strips the package qualifier from an ssa function name.
func GoName(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
