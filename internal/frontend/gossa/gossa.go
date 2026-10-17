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

var specRe = regexp.MustCompile(`go2lean:spec\s+(\w+)`)

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

	// Pass 1: pick candidates and assign FuncIds so calls can be resolved.
	var fns []*ssa.Function
	for _, m := range spkg.Members {
		if f, ok := m.(*ssa.Function); ok && !isInit(f.Name()) && f.Synthetic == "" && !isTestFunc(f.Name()) {
			fns = append(fns, f)
		}
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].Name() < fns[j].Name() })
	for _, f := range fns {
		st.ids[f] = len(p.Funcs)
		p.Funcs = append(p.Funcs, &ir.Func{Name: f.Name(), GoSig: f.Signature.String()})
	}
	// Pass 2: bodies.
	for _, f := range fns {
		out := p.Funcs[st.ids[f]]
		t := &fnState{prog: st, f: f, out: out, vars: map[ssa.Value]ir.Var{}, blockID: map[*ssa.BasicBlock]int{}, addrs: map[ssa.Value]addr{}}
		if err := t.translate(); err != nil {
			rejectStub(out, err)
		}
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("internal error: generated program is malformed: %w", err)
	}
	return p, nil
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
	prog *ir.Prog
	ids  map[*ssa.Function]int
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

type addr struct {
	slice, idx ir.Var
	width      int
}

type fnState struct {
	prog    *progState
	f       *ssa.Function
	out     *ir.Func
	vars    map[ssa.Value]ir.Var
	nvals   int
	blockID map[*ssa.BasicBlock]int
	addrs   map[ssa.Value]addr
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
	t.out.SpecFor = specFor(f)
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
	if !isInt && !(isBool(x.X.Type()) && (x.Op == token.EQL || x.Op == token.NEQ)) {
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

func (t *fnState) havoc(x ssa.Value, site, reason, goHint string) error {
	w, _, ok := intWidth(x.Type())
	if !ok {
		return fmt.Errorf("cannot havoc a value of type %s", x.Type())
	}
	idx := t.prog.newHavoc(w, t.f.Name()+": "+site, reason, goHint)
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
		w, _, ok := sliceElem(x.X.Type())
		if !ok {
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
		t.addrs[x] = addr{slice: s, idx: i, width: w}
		return nil
	case *ssa.Slice:
		return t.slice(x)
	case *ssa.Store:
		if _, ok := t.addrs[x.Addr]; ok {
			t.issue("store to slice element %s ignored (read-only slice model)", x.Addr.Name())
			return nil
		}
		return fmt.Errorf("store through %s not supported", x.Addr.Name())
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
			return fmt.Errorf("multiple results not supported")
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
	case token.MUL: // load
		if a, ok := t.addrs[x.X]; ok {
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Load{Width: a.width}, Args: []ir.Var{a.slice, a.idx}})
			return nil
		}
		return t.havoc(x, "load "+x.X.Name(), "unmodeled-load", "")
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

func abiOf(f *ssa.Function) (*contract.ABI, error) {
	abi := &contract.ABI{GoFunc: f.String(), GoSig: f.Signature.String(), Frontend: Frontend}
	for _, p := range f.Params {
		name := p.Name()
		switch {
		case isBool(p.Type()):
			abi.Params = append(abi.Params, contract.ABIParam{Go: name, GoType: p.Type().String(), Model: []string{name}, Kind: contract.ABIBool})
		default:
			if _, _, ok := intWidth(p.Type()); ok {
				abi.Params = append(abi.Params, contract.ABIParam{Go: name, GoType: p.Type().String(), Model: []string{name}, Kind: contract.ABIInt})
			} else if _, _, ok := sliceElem(p.Type()); ok {
				abi.Params = append(abi.Params, contract.ABIParam{
					Go: name, GoType: p.Type().String(), Kind: contract.ABISlice,
					Model: []string{name + "_ref", name + "_off", name + "_len", name + "_cap"},
					Elem:  p.Type().Underlying().(*types.Slice).Elem().String(),
				})
			} else {
				return nil, fmt.Errorf("parameter %s has unsupported type %s", name, p.Type())
			}
		}
	}
	res := f.Signature.Results()
	switch res.Len() {
	case 0:
	case 1:
		rt := res.At(0).Type()
		_, _, isInt := intWidth(rt)
		if !isInt && !isBool(rt) {
			return nil, fmt.Errorf("result type %s not supported", rt)
		}
		abi.Result.GoType = rt.String()
	default:
		return nil, fmt.Errorf("multiple results not supported")
	}
	return abi, nil
}

func specFor(f *ssa.Function) string {
	decl, ok := f.Syntax().(*ast.FuncDecl)
	if !ok || decl.Doc == nil {
		return ""
	}
	if m := specRe.FindStringSubmatch(decl.Doc.Text()); m != nil {
		return m[1]
	}
	return ""
}

// GoName strips the package qualifier from an ssa function name.
func GoName(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
