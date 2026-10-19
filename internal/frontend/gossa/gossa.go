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
	"hash/fnv"
	"path/filepath"
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
	return TranslateAllOpts(dir, Options{}, patterns...)
}

// Options tune TranslateAll.
type Options struct {
	// Deps also models the functions of dependency packages (outside the
	// standard library) that the compiled packages reach through static
	// calls, as small extra programs the search driver links in; without
	// it those calls are unmodelled (havoc results).
	Deps bool
}

// Limits of the dependency walk: how deep it follows calls from the
// compiled packages, and how many functions it models at most.
const (
	depDepth = 4
	depLimit = 1500
)

// TranslateAllOpts is TranslateAll with options.
func TranslateAllOpts(dir string, opts Options, patterns ...string) ([]*ir.Prog, string, error) {
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
	build := ssautil.Packages
	if opts.Deps {
		// Bodies for the dependencies too: Packages creates those from type
		// information only.
		build = ssautil.AllPackages
	}
	sprog, spkgs := build(pkgs, ssa.InstantiateGenerics)
	sprog.Build()

	modPath := ""
	if pkgs[0].Module != nil {
		modPath = pkgs[0].Module.Path
	}
	// Cross-package references: every candidate function of every package
	// of this compile, so a call into a sibling package links to its model
	// instead of becoming a havoc value.
	// Keyed by name: a package compiled as its test variant holds other
	// function objects than the plain variant its importers call.
	xrefs := map[string]contract.XCall{}
	type unit struct {
		path string
		fns  []*ssa.Function
	}
	var units []unit
	mainPaths := map[string]bool{}
	var roots []*ssa.Function
	for i, spkg := range spkgs {
		if spkg == nil {
			continue
		}
		fns := candidates(spkg)
		units = append(units, unit{pkgs[i].PkgPath, fns})
		mainPaths[pkgs[i].PkgPath] = true
		roots = append(roots, fns...)
	}
	if opts.Deps {
		deps := depClosure(roots, mainPaths)
		var paths []string
		for path := range deps {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			fns := deps[path]
			sort.Slice(fns, func(i, j int) bool { return funcName(fns[i]) < funcName(fns[j]) })
			units = append(units, unit{path, fns})
		}
	}
	for _, u := range units {
		for id, f := range u.fns {
			xrefs[f.String()] = contract.XCall{Package: u.path, ID: id}
		}
	}
	var progs []*ir.Prog
	for _, u := range units {
		p, err := translatePackage(u.path, u.fns, xrefs)
		if err != nil {
			return nil, "", err
		}
		progs = append(progs, p)
	}
	return progs, modPath, nil
}

// depClosure walks the static calls of roots into packages outside the
// compile and the standard library, up to depDepth calls deep and depLimit
// functions, and returns the functions reached by package path. Generic
// functions, wrappers and closures are left out (they stay unmodelled).
func depClosure(roots []*ssa.Function, mainPaths map[string]bool) map[string][]*ssa.Function {
	out := map[string][]*ssa.Function{}
	seen := map[*ssa.Function]bool{}
	count := 0
	var visit func(f *ssa.Function, depth int)
	visit = func(f *ssa.Function, depth int) {
		if depth > depDepth {
			return
		}
		for _, b := range f.Blocks {
			for _, in := range b.Instrs {
				call, ok := in.(ssa.CallInstruction)
				if !ok {
					continue
				}
				callee := call.Common().StaticCallee()
				if callee == nil || seen[callee] || callee.Pkg == nil || callee.Synthetic != "" ||
					callee.Parent() != nil || callee.TypeParams().Len() > 0 || len(callee.TypeArgs()) > 0 ||
					isInit(callee.Name()) || len(callee.Blocks) == 0 {
					continue
				}
				path := callee.Pkg.Pkg.Path()
				if mainPaths[path] || isStdlib(path) {
					continue
				}
				if _, std := ir.StdlibOps[callee.String()]; std {
					continue
				}
				if count >= depLimit {
					return
				}
				seen[callee] = true
				count++
				out[path] = append(out[path], callee)
				visit(callee, depth+1)
			}
		}
	}
	for _, f := range roots {
		visit(f, 1)
	}
	return out
}

// isStdlib says whether an import path belongs to the standard library
// (no dot in its first element).
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// candidates picks the functions a package's model holds (package-level
// functions and the methods of the package's named types), in the order of
// their FuncIds.
func candidates(spkg *ssa.Package) []*ssa.Function {
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
	return fns
}

func translatePackage(pkgPath string, fns []*ssa.Function, xrefs map[string]contract.XCall) (*ir.Prog, error) {
	p := &ir.Prog{Package: pkgPath, Frontend: Frontend}
	st := &progState{prog: p, ids: map[*ssa.Function]int{}, xrefs: xrefs}

	// Pass 1: assign FuncIds so calls can be resolved.
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
	xrefs   map[string]contract.XCall // functions of sibling packages, by name
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
	// quiet suppresses issues while the fallback block of a call is built:
	// its havoc is a contingency, not a gap in the model.
	quiet bool
}

func (t *fnState) fresh() ir.Var {
	v := ir.Var(t.nvals)
	t.nvals++
	return v
}

func (t *fnState) issue(format string, args ...any) {
	if t.quiet {
		return
	}
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
	switch x.(type) {
	case *ssa.Function, *ssa.Builtin:
		// A function used as a value: opaque, like any func-typed value,
		// and never nil.
		v := t.fresh()
		t.vars[x] = v
		t.emit(ir.Instr{Dst: v, Op: ir.Const{V: ir.OpaqueNonNil{}}})
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
	if sv, ok := cv.(ir.StrConst); ok {
		if !slices.Contains(t.out.StrConsts, string(sv.Bytes)) {
			t.out.StrConsts = append(t.out.StrConsts, string(sv.Bytes))
		}
		// A string literal is an allocating instruction, not a constant value.
		t.emit(ir.Instr{Dst: v, Op: sv})
		return v, nil
	}
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
		case isNilable(c.Type()):
			return ir.OpaqueNil{}, nil
		case isOpaqueType(c.Type()):
			return ir.Opaque{}, nil
		}
		// go/ssa writes the zero value of any type as a nil constant.
		if z, ok := zeroVal(c.Type()); ok {
			return z, nil
		}
		return nil, fmt.Errorf("nil constant of type %s", c.Type())
	}
	if isBool(c.Type()) {
		return ir.BoolVal(constant.BoolVal(c.Value)), nil
	}
	if isString(c.Type()) {
		return ir.StrConst{Bytes: []byte(constant.StringVal(c.Value))}, nil
	}
	if isOpaqueType(c.Type()) {
		return ir.Opaque{}, nil // a float literal: the model does not compute with it
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
	if isString(x.X.Type()) {
		switch x.Op {
		case token.ADD:
			op = ir.Simple("strConcat")
		case token.EQL, token.NEQ:
		default:
			return fmt.Errorf("string operator %s not supported", x.Op)
		}
	} else if isOpaqueType(x.X.Type()) {
		if eqOp && isNilable(x.X.Type()) {
			if v, ok := nilOperand(x); ok {
				return t.nilCompare(x, v)
			}
		}
		// Comparing interfaces, maps, funcs or channels with anything but
		// nil, or arithmetic on floats: the model does not look inside
		// them, so the outcome is havoc (a bool, or an opaque number).
		return t.havoc(x, fmt.Sprintf("%s on %s", x.Op, x.X.Type()), "opaque-op", "")
	} else if !isInt && !(eqOp && (isBool(x.X.Type()) || isError(x.X.Type()) || isPointer(x.X.Type()) || isSlice(x.X.Type()))) {
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
	case isString(t):
		return ir.HavocString, true
	}
	w, _, ok := intWidth(t)
	return w, ok
}

// assumedValue is what an unmodelled call's result becomes when it cannot
// be havoc'd: the zero value, or an opaque value for interfaces, maps and
// funcs. The function is marked partial with the assumption spelled out;
// this is an under-approximation M3 may expose as spurious results.
func assumedValue(t types.Type) (ir.Val, bool) {
	if isNilable(t) {
		return ir.Opaque{}, true // unknown, not the nil zero value
	}
	if z, ok := zeroVal(t); ok {
		return z, true
	}
	return nil, false
}

// isNilable says whether t is a func, map, channel, interface (other than
// error, modelled on its own) or unsafe.Pointer type: an opaque type whose
// values the model tells apart only as nil, non-nil or unknown.
func isNilable(t types.Type) bool {
	if isError(t) {
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Interface, *types.Map, *types.Signature, *types.Chan:
		return true
	case *types.Basic:
		return u.Kind() == types.UnsafePointer
	}
	return false
}

func (t *fnState) havoc(x ssa.Value, site, reason, goHint string) error {
	if tu, ok := x.Type().(*types.Tuple); ok {
		// A multi-result call: havoc each component and pack them.
		var parts []ir.Var
		for i := 0; i < tu.Len(); i++ {
			v := t.fresh()
			if err := t.havocInto(v, tu.At(i).Type(), fmt.Sprintf("%s: %s #%d", t.f.Name(), site, i), reason, goHint); err != nil {
				return err
			}
			parts = append(parts, v)
		}
		t.issue("%s: %s is havoc (%s)", site, x.Name(), reason)
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("mkTuple"), Args: parts})
		return nil
	}
	if err := t.havocInto(t.def(x), x.Type(), t.f.Name()+": "+site, reason, goHint); err != nil {
		return err
	}
	t.issue("%s: %s is havoc (%s)", site, x.Name(), reason)
	return nil
}

// havocInto binds dst to a havoc value of type ty, or to an assumed value
// when the type cannot be havoc'd: a pointer to a fresh zero object (nil
// would make every later dereference a spurious panic), a slice of a small
// havoc'd length holding zero values, else the zero or opaque value.
func (t *fnState) havocInto(dst ir.Var, ty types.Type, site, reason, goHint string) error {
	if k, ok := havocKind(ty); ok {
		idx := t.prog.newHavoc(k, site, reason, goHint)
		t.emit(ir.Instr{Dst: dst, HavocSite: idx})
		return nil
	}
	switch u := ty.Underlying().(type) {
	case *types.Pointer:
		if zero, ok := zeroVal(u.Elem()); ok {
			t.issue("%s: assumed a pointer to a fresh zero %s (not modelled)", site, u.Elem())
			t.emit(ir.Instr{Dst: dst, Op: ir.Alloc{Zero: zero}})
			return nil
		}
	case *types.Slice:
		zero, ok := zeroVal(u.Elem())
		if !ok {
			zero, ok = assumedValue(u.Elem())
		}
		if ok {
			t.issue("%s: assumed a slice of 0..3 zero values of %s (not modelled)", site, u.Elem())
			raw := t.fresh()
			t.emit(ir.Instr{Dst: raw, HavocSite: t.prog.newHavoc(64, site+" (length)", reason, goHint)})
			four := t.fresh()
			t.emit(ir.Instr{Dst: four, Op: ir.Const{V: ir.IntVal{Width: 64, Bits: "4"}}})
			n := t.fresh()
			t.emit(ir.Instr{Dst: n, Op: ir.Simple("urem"), Args: []ir.Var{raw, four}})
			t.emit(ir.Instr{Dst: dst, Op: ir.MakeSlice{Zero: zero}, Args: []ir.Var{n, n}})
			return nil
		}
	}
	v, ok := assumedValue(ty)
	if !ok {
		return fmt.Errorf("cannot havoc a value of type %s", ty)
	}
	t.issue("%s: assumed the zero value of %s (not modelled)", site, ty)
	t.emit(ir.Instr{Dst: dst, Op: ir.Const{V: v}})
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
		if !isSlice(x.X.Type()) && !isPointerToArray(x.X.Type()) {
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
		elem := x.Type().(*types.Pointer).Elem()
		zero, ok := zeroVal(elem)
		if !ok {
			if zero, ok = assumedValue(elem); !ok {
				return fmt.Errorf("alloc of unsupported type %s", elem)
			}
			t.issue("alloc of %s is opaque (not modelled)", elem)
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Alloc{Zero: zero}})
		return nil
	case *ssa.MakeInterface:
		// The value is boxed with a tag for its dynamic type, so a type
		// assertion back to that type gets it out again.
		if v, err := t.val(x.X); err == nil {
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Box{Tag: typeTag(x.X.Type())}, Args: []ir.Var{v}})
			return nil
		}
		t.issue("interface value %s is opaque (not modelled)", x.Name())
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.OpaqueNonNil{}}})
		return nil
	case *ssa.MakeMap:
		t.issue("map %s is opaque (not modelled)", x.Name())
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.OpaqueNonNil{}}})
		return nil
	case *ssa.MakeClosure:
		// A closure is a func value: opaque, and a call through it is an
		// unmodelled call (havoc results, effects ignored).
		t.issue("closure %s is opaque (not modelled)", x.Fn.Name())
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.OpaqueNonNil{}}})
		return nil
	case *ssa.MakeChan:
		t.issue("channel %s is opaque (not modelled)", x.Name())
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.OpaqueNonNil{}}})
		return nil
	case *ssa.MapUpdate:
		t.issue("map update %s[%s] ignored (maps are opaque)", x.Map.Name(), x.Key.Name())
		return nil
	case *ssa.Defer:
		// A deferred call to a function in the model would need the defer
		// stack; one the model does not look into (mutex unlocks, closes,
		// closures) is an effect it does not model anyway.
		if callee := x.Call.StaticCallee(); callee != nil {
			if _, modelled := t.prog.ids[callee]; modelled {
				return fmt.Errorf("deferred call to %s (defer of a modelled function)", callee.Name())
			}
		}
		t.issue("deferred call %s ignored (effects not modelled)", x.Call.String())
		return nil
	case *ssa.RunDefers:
		return nil
	case *ssa.Range:
		if isString(x.X.Type()) {
			// The iterator is a heap cell (s, pos) that strNext advances.
			s, err := t.val(x.X)
			if err != nil {
				return err
			}
			zero := t.fresh()
			t.emit(ir.Instr{Dst: zero, Op: ir.Const{V: ir.IntVal{Width: 64, Bits: "0"}}})
			state := t.fresh()
			t.emit(ir.Instr{Dst: state, Op: ir.Simple("mkTuple"), Args: []ir.Var{s, zero}})
			cell := t.def(x)
			t.emit(ir.Instr{Dst: cell, Op: ir.Alloc{Zero: ir.Opaque{}}})
			t.emit(ir.Instr{Dst: t.fresh(), Op: ir.Simple("storePath"), Args: []ir.Var{cell, state}})
			return nil
		}
		if _, isMap := x.X.Type().Underlying().(*types.Map); isMap {
			t.issue("range over map %s assumed empty (maps are opaque)", x.X.Name())
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.Opaque{}}})
			return nil
		}
		return fmt.Errorf("range over %s not supported", x.X.Type())
	case *ssa.Next:
		it, err := t.val(x.Iter)
		if err != nil {
			return err
		}
		if x.IsString {
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("strNext"), Args: []ir.Var{it}})
			return nil
		}
		// An opaque map: no iterations (recorded at the Range).
		tu := x.Type().(*types.Tuple)
		var parts []ir.Var
		for i := 0; i < tu.Len(); i++ {
			v := t.fresh()
			var zero ir.Val = ir.BoolVal(false)
			if i > 0 {
				var ok bool
				if zero, ok = assumedValue(tu.At(i).Type()); !ok {
					zero = ir.Opaque{} // an unused component has the invalid type
				}
			}
			t.emit(ir.Instr{Dst: v, Op: ir.Const{V: zero}})
			parts = append(parts, v)
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("mkTuple"), Args: parts})
		return nil
	case *ssa.ChangeInterface:
		// Converting between interface types keeps the value: nil stays
		// nil, a boxed value keeps its dynamic type.
		v, err := t.val(x.X)
		if err != nil {
			return err
		}
		t.vars[x] = v
		return nil
	case *ssa.TypeAssert:
		if zero, ok := zeroVal(x.AssertedType); ok && !types.IsInterface(x.AssertedType) {
			return t.typeAssert(x, zero)
		}
		if x.CommaOk {
			return t.havoc(x, "type assertion", "opaque-assert", "")
		}
		// A failing single-result assertion panics in Go; the model assumes
		// it succeeds and havocs the result, so that panic is not found.
		return t.havoc(x, "type assertion (assumed to succeed)", "opaque-assert", "")
	case *ssa.Index:
		// Index covers arrays and, in recent go/ssa, strings.
		a, err := t.val(x.X)
		if err != nil {
			return err
		}
		i, err := t.val(x.Index)
		if err != nil {
			return err
		}
		op := ir.Simple("arrIndex")
		if isString(x.X.Type()) {
			op = ir.Simple("strIndex")
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: op, Args: []ir.Var{a, i}})
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
	case *ssa.Lookup:
		if _, isMap := x.X.Type().Underlying().(*types.Map); isMap {
			return t.havoc(x, "map lookup", "opaque-map", "")
		}
		if !isString(x.X.Type()) || x.CommaOk {
			return fmt.Errorf("lookup on %s not supported", x.X.Type())
		}
		s, err := t.val(x.X)
		if err != nil {
			return err
		}
		i, err := t.val(x.Index)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("strIndex"), Args: []ir.Var{s, i}})
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
		t.recordExit(x)
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
		if isOpaqueType(x.X.Type()) {
			return t.havoc(x, fmt.Sprintf("%s on %s", x.Op, x.X.Type()), "opaque-op", "")
		}
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
	if isString(dst.Type()) && isByteSlice(src.Type()) || isByteSlice(dst.Type()) && isString(src.Type()) {
		a, err := t.val(src)
		if err != nil {
			return err
		}
		op := ir.Simple("bytesToStr")
		if isString(src.Type()) {
			op = ir.Simple("strToBytes")
		}
		t.emit(ir.Instr{Dst: t.def(dst), Op: op, Args: []ir.Var{a}})
		return nil
	}
	if isString(dst.Type()) && isString(src.Type()) || types.Identical(dst.Type().Underlying(), src.Type().Underlying()) {
		// A named string type, or a change of name only (json.RawMessage
		// <-> []byte): the value is unchanged.
		a, err := t.val(src)
		if err != nil {
			return err
		}
		t.vars[dst] = a
		return nil
	}
	if isRuneSlice(dst.Type()) && isString(src.Type()) || isString(dst.Type()) && isRuneSlice(src.Type()) {
		a, err := t.val(src)
		if err != nil {
			return err
		}
		op := ir.Simple("runesToStr")
		if isString(src.Type()) {
			op = ir.Simple("strToRunes")
		}
		t.emit(ir.Instr{Dst: t.def(dst), Op: op, Args: []ir.Var{a}})
		return nil
	}
	if _, _, isInt := intWidth(src.Type()); isInt && isString(dst.Type()) {
		a, err := t.val(src)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(dst), Op: ir.Simple("runeToStr"), Args: []ir.Var{a}})
		return nil
	}
	if isOpaqueType(dst.Type()) || isOpaqueType(src.Type()) {
		// int <-> float and the like: the model does not compute with floats.
		return t.havoc(dst, fmt.Sprintf("conversion %s -> %s", src.Type(), dst.Type()), "opaque-op", "")
	}
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
	str := isString(x.X.Type())
	if !isSlice(x.X.Type()) && !str && !isPointerToArray(x.X.Type()) {
		return fmt.Errorf("reslice of non-slice type %s", x.X.Type())
	}
	if x.Max != nil {
		if str {
			return fmt.Errorf("three-index slice of a string")
		}
		s, err := t.val(x.X)
		if err != nil {
			return err
		}
		lo := t.fresh()
		t.emit(ir.Instr{Dst: lo, Op: ir.Const{V: ir.IntVal{Width: 64, Bits: "0"}}})
		if x.Low != nil {
			if lo, err = t.val(x.Low); err != nil {
				return err
			}
		}
		hi, err := t.val(x.High)
		if err != nil {
			return err
		}
		mx, err := t.val(x.Max)
		if err != nil {
			return err
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("reslice3"), Args: []ir.Var{s, lo, hi, mx}})
		return nil
	}
	lenOp, sliceOp := ir.Simple("len"), ir.Simple("reslice")
	if str {
		lenOp, sliceOp = ir.Simple("strLen"), ir.Simple("strSlice")
	}
	if isPointerToArray(x.X.Type()) {
		lenOp = ir.Simple("strLen") // the dialect reads an array's length through this op too
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
		t.emit(ir.Instr{Dst: hi, Op: lenOp, Args: []ir.Var{s}})
	}
	t.emit(ir.Instr{Dst: t.def(x), Op: sliceOp, Args: []ir.Var{s, lo, hi}})
	return nil
}

func (t *fnState) call(x *ssa.Call) error {
	hasResult := !isUnitTuple(x.Type())
	if bi, ok := x.Call.Value.(*ssa.Builtin); ok {
		switch bi.Name() {
		case "len", "cap":
			if isSlice(x.Call.Args[0].Type()) {
				a, err := t.val(x.Call.Args[0])
				if err != nil {
					return err
				}
				t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple(bi.Name()), Args: []ir.Var{a}})
				return nil
			}
			if bi.Name() == "len" && isPointerToArray(x.Call.Args[0].Type()) {
				a, err := t.val(x.Call.Args[0])
				if err != nil {
					return err
				}
				t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("strLen"), Args: []ir.Var{a}})
				return nil
			}
			if bi.Name() == "len" && isString(x.Call.Args[0].Type()) {
				a, err := t.val(x.Call.Args[0])
				if err != nil {
					return err
				}
				t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("strLen"), Args: []ir.Var{a}})
				return nil
			}
		case "append", "copy":
			// append(s, t...) and copy(dst, src) on slices of modelled
			// elements; `append([]byte, string...)` and `copy([]byte, string)`
			// go through a byte-slice copy of the string.
			if !isSlice(x.Call.Args[0].Type()) {
				break
			}
			a, err := t.val(x.Call.Args[0])
			if err != nil {
				return err
			}
			b, err := t.val(x.Call.Args[1])
			if err != nil {
				return err
			}
			if isString(x.Call.Args[1].Type()) {
				bs := t.fresh()
				t.emit(ir.Instr{Dst: bs, Op: ir.Simple("strToBytes"), Args: []ir.Var{b}})
				b = bs
			}
			if bi.Name() == "copy" {
				t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("copySlice"), Args: []ir.Var{a, b}})
				return nil
			}
			zero, ok := zeroVal(x.Type().Underlying().(*types.Slice).Elem())
			if !ok {
				return fmt.Errorf("append to unsupported slice type %s", x.Type())
			}
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Append{Zero: zero}, Args: []ir.Var{a, b}})
			return nil
		}
		if hasResult {
			if bi.Name() == "len" || bi.Name() == "cap" {
				// The length of a map or channel the model does not look
				// into: havoc, but kept below 16 so that `make([]T, 0, len(m))`
				// stays something the model materialises. Recorded as an
				// assumption.
				t.issue("%s of %s assumed below 16 (not modelled)", bi.Name(), x.Call.Args[0].Type())
				raw := t.fresh()
				t.emit(ir.Instr{Dst: raw, HavocSite: t.prog.newHavoc(64, fmt.Sprintf("%s: %s", t.f.Name(), bi.Name()), "unmodeled-call", "")})
				sixteen := t.fresh()
				t.emit(ir.Instr{Dst: sixteen, Op: ir.Const{V: ir.IntVal{Width: 64, Bits: "16"}}})
				t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("urem"), Args: []ir.Var{raw, sixteen}})
				return nil
			}
			return t.havoc(x, "builtin "+bi.Name(), "unmodeled-call", "")
		}
		t.issue("call to builtin %s ignored", bi.Name())
		return nil
	}
	callee := x.Call.StaticCallee()
	if callee != nil {
		// Ops without a result (Builder.Reset) return the unit value.
		if op, ok := ir.StdlibOps[callee.String()]; ok {
			var args []ir.Var
			for _, a := range x.Call.Args {
				v, err := t.val(a)
				if err != nil {
					return err
				}
				args = append(args, v)
			}
			t.emit(ir.Instr{Dst: t.def(x), Op: op, Args: args})
			return nil
		}
	}
	if id, ok := t.prog.ids[callee]; ok && callee != nil {
		if !slices.Contains(t.out.Calls, id) {
			t.out.Calls = append(t.out.Calls, id)
		}
		return t.emitCall(x, ir.Term{Kind: ir.TermCall, Callee: id}, callee.String())
	}
	if callee == nil {
		// fall through to the unmodelled cases below
	} else if xr, ok := t.prog.xrefs[callee.String()]; ok && xr.Package != t.prog.prog.Package {
		if !slices.Contains(t.out.XCalls, xr) {
			t.out.XCalls = append(t.out.XCalls, xr)
		}
		return t.emitCall(x, ir.Term{Kind: ir.TermXCall, Callee: xr.ID, CalleePkg: xr.Package}, callee.String())
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

// recordExit notes a return statement: its block (the current one), its
// source line, and whether it returns the constant true.
func (t *fnState) recordExit(x *ssa.Return) {
	block := -1
	for i := range t.out.Blocks {
		if &t.out.Blocks[i] == t.cur {
			block = i
		}
	}
	line := 0
	if t.f.Prog != nil && x.Pos().IsValid() {
		line = t.f.Prog.Fset.Position(x.Pos()).Line
	}
	constTrue := false
	if len(x.Results) == 1 {
		if c, ok := x.Results[0].(*ssa.Const); ok && c.Value != nil && isBool(c.Type()) && constant.BoolVal(c.Value) {
			constTrue = true
		}
	}
	t.out.Exits = append(t.out.Exits, contract.ExitSite{Block: block, Line: line, ConstTrue: constTrue})
}

// stringEnum returns the string constants declared with type t in t's
// package, for a named string type (at most 64, sorted); nil otherwise.
func stringEnum(t types.Type) []string {
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return nil
	}
	scope := n.Obj().Pkg().Scope()
	var out []string
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok || !types.Identical(c.Type(), t) || c.Val().Kind() != constant.String {
			continue
		}
		v := constant.StringVal(c.Val())
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// typeTag names a dynamic type in boxed interface values: a hash of the
// type's fully qualified name, so every package of a linked model agrees.
func typeTag(t types.Type) uint32 {
	h := fnv.New32a()
	h.Write([]byte(types.TypeString(t, nil)))
	return h.Sum32()
}

// typeAssert lowers x.(T) for a concrete T. A value the model boxed itself
// is unboxed, a nil interface fails; a failure gives the zero value and
// false, or panics. An interface value the model knows nothing about (the
// result of an unmodelled call) gives a havoc result.
func (t *fnState) typeAssert(x *ssa.TypeAssert, zero ir.Val) error {
	v, err := t.val(x.X)
	if err != nil {
		return err
	}
	dst := t.def(x)
	isBoxed := t.fresh()
	t.emit(ir.Instr{Dst: isBoxed, Op: ir.Simple("nilKnown"), Args: []ir.Var{v}})
	boxed := len(t.out.Blocks)
	other, join := boxed+1, boxed+2
	t.cur.Term = ir.Term{Kind: ir.TermBr, Cond: isBoxed, T: boxed, F: other}
	name := t.cur.Name
	t.out.Blocks = append(t.out.Blocks,
		ir.Block{Name: name + ".assert-boxed"},
		ir.Block{Name: name + ".assert-unknown"},
		ir.Block{Name: name + ".assert-join"})
	t.cur = &t.out.Blocks[boxed]
	t.emit(ir.Instr{Dst: dst, Op: ir.Unbox{Tag: typeTag(x.AssertedType), CommaOk: x.CommaOk, Zero: zero}, Args: []ir.Var{v}})
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: join}
	t.cur = &t.out.Blocks[other]
	t.issue("type assertion %s on an interface value the model did not build is havoc", x.Name())
	if err := t.havocResult(dst, x, "type assertion", "opaque-assert"); err != nil {
		return err
	}
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: join}
	t.cur = &t.out.Blocks[join]
	return nil
}

// nilOperand returns the operand compared with a nil constant in x, if
// one side of x is that constant.
func nilOperand(x *ssa.BinOp) (ssa.Value, bool) {
	if c, ok := x.Y.(*ssa.Const); ok && c.Value == nil {
		return x.X, true
	}
	if c, ok := x.X.(*ssa.Const); ok && c.Value == nil {
		return x.Y, true
	}
	return nil, false
}

// nilCompare lowers v == nil or v != nil for a func, map, channel or
// interface value: decided when the model knows the value's nil-ness (nil
// inputs, values the program built), havoc otherwise.
func (t *fnState) nilCompare(x *ssa.BinOp, v ssa.Value) error {
	val, err := t.val(v)
	if err != nil {
		return err
	}
	dst := t.def(x)
	known := t.fresh()
	t.emit(ir.Instr{Dst: known, Op: ir.Simple("nilKnown"), Args: []ir.Var{val}})
	yes := len(t.out.Blocks)
	no, join := yes+1, yes+2
	t.cur.Term = ir.Term{Kind: ir.TermBr, Cond: known, T: yes, F: no}
	name := t.cur.Name
	t.out.Blocks = append(t.out.Blocks,
		ir.Block{Name: name + ".nil-known"},
		ir.Block{Name: name + ".nil-unknown"},
		ir.Block{Name: name + ".nil-join"})
	t.cur = &t.out.Blocks[yes]
	if x.Op == token.EQL {
		t.emit(ir.Instr{Dst: dst, Op: ir.Simple("isNilOpaque"), Args: []ir.Var{val}})
	} else {
		isNil := t.fresh()
		t.emit(ir.Instr{Dst: isNil, Op: ir.Simple("isNilOpaque"), Args: []ir.Var{val}})
		t.emit(ir.Instr{Dst: dst, Op: ir.Simple("not"), Args: []ir.Var{isNil}})
	}
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: join}
	t.cur = &t.out.Blocks[no]
	t.issue("nil comparison %s on a value of unknown nil-ness is havoc", x.Name())
	idx := t.prog.newHavoc(ir.HavocBool, t.f.Name()+": nil comparison", "opaque-op", "")
	t.emit(ir.Instr{Dst: dst, HavocSite: idx})
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: join}
	t.cur = &t.out.Blocks[join]
	return nil
}

// emitCall terminates the current block with the call term (its callee
// already set), continuing in a fresh block. A second fresh block is the
// call's fallback: run when the callee gets stuck, it binds the result to
// a havoc value (as an unmodelled call would) and joins the continuation.
// A call without a result binds the unit value, so the callee's effects on
// the heap are modelled too.
func (t *fnState) emitCall(x *ssa.Call, term ir.Term, name string) error {
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
	fb := next + 1
	term.CArgs, term.Dst, term.Next, term.Fallback = args, dst, next, fb
	// Terminate the current block before appending: append may move the
	// backing array and t.cur would then point at a stale copy.
	t.cur.Term = term
	t.out.Blocks = append(t.out.Blocks,
		ir.Block{Name: t.cur.Name + ".after-call"},
		ir.Block{Name: t.cur.Name + ".call-fallback"})
	t.cur = &t.out.Blocks[fb]
	t.quiet = true
	err := t.havocResult(dst, x, "call "+name+" (callee stuck)", "callee-stuck")
	t.quiet = false
	if err != nil {
		return err
	}
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: next}
	t.cur = &t.out.Blocks[next]
	return nil
}

// havocResult binds dst to a havoc value of the call's result type (a
// tuple for several results, unit for none).
func (t *fnState) havocResult(dst ir.Var, x ssa.Value, site, reason string) error {
	if isUnitTuple(x.Type()) {
		t.emit(ir.Instr{Dst: dst, Op: ir.Const{V: ir.UnitVal{}}})
		return nil
	}
	if tu, ok := x.Type().(*types.Tuple); ok {
		var parts []ir.Var
		for i := 0; i < tu.Len(); i++ {
			v := t.fresh()
			if err := t.havocInto(v, tu.At(i).Type(), fmt.Sprintf("%s: %s #%d", t.f.Name(), site, i), reason, ""); err != nil {
				return err
			}
			parts = append(parts, v)
		}
		t.emit(ir.Instr{Dst: dst, Op: ir.Simple("mkTuple"), Args: parts})
		return nil
	}
	return t.havocInto(dst, x.Type(), t.f.Name()+": "+site, reason, "")
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

func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

func isRuneSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := s.Elem().Underlying().(*types.Basic)
	return ok && (b.Kind() == types.Int32 || b.Kind() == types.Rune)
}

func isByteSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := s.Elem().Underlying().(*types.Basic)
	return ok && (b.Kind() == types.Uint8 || b.Kind() == types.Byte)
}

func isPointer(t types.Type) bool {
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

func isPointerToArray(t types.Type) bool {
	p, ok := t.Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	_, ok = p.Elem().Underlying().(*types.Array)
	return ok
}

func isSlice(t types.Type) bool {
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

// isOpaqueType says whether t is modelled as an opaque value: interfaces,
// maps, funcs, channels, and the basic types the dialect has no arithmetic
// for (floats, complex numbers, unsafe pointers).
func isOpaqueType(t types.Type) bool {
	if isError(t) {
		return false // the error interface is modelled precisely (nil or not)
	}
	switch u := t.Underlying().(type) {
	case *types.Interface, *types.Map, *types.Signature, *types.Chan:
		return true
	case *types.Basic:
		switch u.Kind() {
		case types.Float32, types.Float64, types.Complex64, types.Complex128, types.UnsafePointer,
			types.UntypedFloat, types.UntypedComplex:
			return true
		}
	}
	return false
}

// isStringsBuilder says whether t is strings.Builder, which the model keeps
// as a string cell (its methods are dialect ops).
func isStringsBuilder(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "strings" && n.Obj().Name() == "Builder"
}

// isForeignUnexported says whether t is an unexported named type of
// another package (gin's `errorMsgs`): a test in the target package cannot
// name it, so values of it are opaque parameters with the zero value.
func isForeignUnexported(pkg *types.Package, t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg() != pkg && !n.Obj().Exported()
}

// isForeignOpaqueStruct says whether t is a struct of another package with
// no exported field (sync.Mutex, strings.Builder): a test cannot build one
// with contents, so it is an opaque parameter with the zero value.
func isForeignOpaqueStruct(pkg *types.Package, t types.Type) bool {
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg() == pkg {
		return false
	}
	st, ok := n.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Exported() {
			return false
		}
	}
	return true
}

// zeroLit is the Go literal of t's zero value where the harness needs one
// for an opaque element: "0" for numbers, "nil" for everything else.
func zeroLit(t types.Type, src string) string {
	if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&types.IsNumeric != 0 {
		return "0"
	}
	if _, ok := t.Underlying().(*types.Struct); ok {
		return src + "{}"
	}
	return "nil"
}

// abiArrayLimit bounds the arrays described element by element in the
// ABI; longer ones are opaque parameters (zero in M3, `.unit` in the model).
const abiArrayLimit = 64

// isTestingType says whether t is (a pointer to) a type of package testing:
// a function taking one is a test helper, not a target.
func isTestingType(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "testing"
}

// valueOK says whether values of type t are representable in the model:
// integers, bools, errors, strings, and pointers to, structs of and slices
// of such values. Interfaces, maps, funcs and channels are representable
// as opaque values: the model never looks inside them, every read through
// them is havoc, and M3 passes their zero value. Recursive types are fine: a pointer
// cycle is cut by treating the type under construction as representable.
func valueOK(t types.Type) bool { return valueOKVisiting(t, map[types.Type]bool{}) }

func valueOKVisiting(t types.Type, visiting map[types.Type]bool) bool {
	if _, _, ok := intWidth(t); ok || isBool(t) || isError(t) || isString(t) || isOpaqueType(t) {
		return true
	}
	if visiting[t] {
		return true
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return valueOKVisiting(u.Elem(), visiting)
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if !valueOKVisiting(u.Field(i).Type(), visiting) {
				return false
			}
		}
		return true
	case *types.Slice:
		return valueOKVisiting(u.Elem(), visiting)
	case *types.Array:
		return u.Len() == 0 || valueOKVisiting(u.Elem(), visiting)
	}
	return false
}

// zeroVal is the model's zero value of t.
func zeroVal(t types.Type) (ir.Val, bool) {
	if w, _, ok := intWidth(t); ok {
		return ir.IntVal{Width: w, Bits: "0"}, true
	}
	if isStringsBuilder(t) {
		return ir.EmptyStr{}, true // a Builder is a string cell in the model
	}
	if isBool(t) {
		return ir.BoolVal(false), true
	}
	if isError(t) {
		return ir.ErrVal{}, true
	}
	if isString(t) {
		return ir.EmptyStr{}, true
	}
	if isNilable(t) {
		return ir.OpaqueNil{}, true
	}
	if isOpaqueType(t) {
		return ir.Opaque{}, true
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
	case *types.Array:
		if u.Len() > 4096 {
			return nil, false
		}
		z, ok := zeroVal(u.Elem())
		if !ok {
			// Arrays of interfaces (varargs) hold opaque values.
			if z, ok = assumedValue(u.Elem()); !ok {
				return nil, false
			}
		}
		elems := make([]ir.Val, u.Len())
		for i := range elems {
			elems[i] = z
		}
		return ir.ArrVal{Elems: elems}, true
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
	src, paths := spellAll(pkg, t)
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg() != pkg {
		importPath = n.Obj().Pkg().Path()
	} else if len(paths) > 0 {
		importPath = paths[0]
	}
	return src, importPath
}

// spellAll is spell with every import path the spelling mentions, in
// order of first appearance: a func or map type names other packages'
// types inside it (`func(http.RoundTripper) http.RoundTripper`).
func spellAll(pkg *types.Package, t types.Type) (src string, paths []string) {
	qual := func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		if !slices.Contains(paths, p.Path()) {
			paths = append(paths, p.Path())
		}
		if recordImport != nil {
			return recordImport(p.Path(), p.Name())
		}
		return p.Name()
	}
	src = types.TypeString(t, qual)
	return src, paths
}

// recordImport, while an ABI is being built, notes every package a
// spelling mentions and returns the qualifier to spell it with: the
// package name, or an alias when another package of that name is already
// in use (two packages called `anthropic`).
var recordImport func(path, name string) string

func abiOf(f *ssa.Function) (*contract.ABI, error) {
	abi := &contract.ABI{GoFunc: f.String(), GoSig: f.Signature.String(), Frontend: Frontend, Package: f.Pkg.Pkg.Path(), Variadic: f.Signature.Variadic(), Imports: map[string]string{}}
	used := map[string]string{} // qualifier -> path
	recordImport = func(path, name string) string {
		alias := name
		for i := 2; ; i++ {
			if p, taken := used[alias]; !taken || p == path {
				break
			}
			alias = fmt.Sprintf("%s%d", name, i)
		}
		used[alias] = path
		if alias != filepath.Base(path) {
			abi.Imports[path] = alias
		}
		return alias
	}
	defer func() { recordImport = nil }()
	if f.Signature.Recv() != nil {
		abi.Method = f.Name()
	}
	pkg := f.Pkg.Pkg
	ab := &abiBuilder{pkg: pkg, abi: abi, index: map[*types.Named]int{}}
	for _, p := range f.Params {
		if isTestingType(p.Type()) {
			return nil, fmt.Errorf("test helper: parameter %s has type %s", p.Name(), p.Type())
		}
		ap, err := ab.param(p.Name(), p.Type(), nil)
		if err != nil {
			return nil, fmt.Errorf("parameter %s has unsupported type %s: %w", p.Name(), p.Type(), err)
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
		r.GoType, r.ExtraPkgs = spellAll(pkg, rt)
		if len(r.ExtraPkgs) > 0 {
			r.TypePkg = r.ExtraPkgs[0]
		}
		_, r.Func = rt.Underlying().(*types.Signature)
		abi.Results = append(abi.Results, r)
	}
	return abi, nil
}

// abiBuilder describes parameters, moving named struct types that refer
// to themselves into the ABI's type table so the description stays finite.
type abiBuilder struct {
	pkg   *types.Package
	abi   *contract.ABI
	index map[*types.Named]int
}

// register adds a recursive named struct type to the table.
func (ab *abiBuilder) register(n *types.Named, src, ipath string) int {
	if idx, ok := ab.index[n]; ok {
		return idx
	}
	idx := len(ab.abi.Types)
	ab.index[n] = idx
	ab.abi.Types = append(ab.abi.Types, contract.ABIType{GoType: src, TypePkg: ipath})
	return idx
}

// fields describes the fields of a struct; unexported fields of a foreign
// struct are opaque, since a test in the target package cannot set them.
func (ab *abiBuilder) fields(u *types.Struct, foreign bool, stack []*types.Named) ([]contract.ABIParam, error) {
	var fields []contract.ABIParam
	for i := 0; i < u.NumFields(); i++ {
		fld := u.Field(i)
		if foreign && !fld.Exported() {
			fields = append(fields, contract.ABIParam{Go: fld.Name(), GoType: types.TypeString(fld.Type(), nil), Model: []string{fld.Name()}, Kind: contract.ABIOpaque, Zero: zeroLit(fld.Type(), types.TypeString(fld.Type(), nil))})
			continue
		}
		f, err := ab.param(fld.Name(), fld.Type(), stack)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", fld.Name(), err)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// param describes one value of type t named name. `stack` holds the named
// struct types under construction; meeting one again means recursion.
func (ab *abiBuilder) param(name string, t types.Type, stack []*types.Named) (contract.ABIParam, error) {
	pkg := ab.pkg
	src, ipath := spell(pkg, t)
	// An alias (`type PairingManager = core.PairingManager`) is spelled by
	// its own name but described as the type it stands for.
	t = types.Unalias(t)
	if n, ok := t.(*types.Named); ok {
		if st, isStruct := n.Underlying().(*types.Struct); isStruct && !isForeignOpaqueStruct(pkg, t) && !isForeignUnexported(pkg, t) {
			// Every named struct lives in the type table once and parameters
			// and fields refer to it: recursion resolves through the index,
			// and a type nested many times (gin.Context) is described once.
			if idx, done := ab.index[n]; done {
				return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIStruct, TypeRef: ab.abi.Types[idx].GoType}, nil
			}
			idx := ab.register(n, src, ipath)
			fields, err := ab.fields(st, ipath != "", stack)
			if err != nil {
				return contract.ABIParam{}, err
			}
			ab.abi.Types[idx].Fields = fields
			return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIStruct, TypeRef: ab.abi.Types[idx].GoType}, nil
		}
	}
	_, allPaths := spellAll(pkg, t)
	extra := []string{}
	for _, ip := range allPaths {
		if ip != ipath {
			extra = append(extra, ip)
		}
	}
	switch {
	case isStringsBuilder(t):
		// An empty Builder (a copied non-empty one panics in Go): a string of
		// length zero in the model, `strings.Builder{}` in the test.
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIString, MaxLen: -1}, nil
	case isOpaqueType(t) || isForeignOpaqueStruct(pkg, t) || isForeignUnexported(pkg, t):
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, ExtraPkgs: extra, Model: []string{name}, Kind: contract.ABIOpaque, Zero: zeroLit(t, src)}, nil
	case isBool(t):
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIBool}, nil
	case isError(t):
		return contract.ABIParam{Go: name, GoType: "error", Model: []string{name}, Kind: contract.ABIError}, nil
	case isString(t):
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIString, Enum: stringEnum(t)}, nil
	}
	if w, signed, ok := intWidth(t); ok {
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Width: w, Signed: signed, Model: []string{name}, Kind: contract.ABIInt}, nil
	}
	// A slice, array or pointer of an unexported type of another package
	// cannot be written down by the test either.
	switch u := t.Underlying().(type) {
	case *types.Array, *types.Slice, *types.Pointer:
		if isForeignUnexported(pkg, u.(interface{ Elem() types.Type }).Elem()) {
			return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIOpaque, Zero: "nil"}, nil
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Array:
		esrc, epath := spell(pkg, u.Elem())
		if u.Len() > abiArrayLimit {
			return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIOpaque}, nil
		}
		ap := contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Kind: contract.ABIArray, Model: []string{name}, Elem: esrc, ElemPkg: epath, Len: int(u.Len())}
		if ew, esigned, ok := intWidth(u.Elem()); ok {
			ap.ElemWidth, ap.ElemSigned = ew, esigned
			return ap, nil
		}
		elem, err := ab.param("elem", u.Elem(), stack)
		if err != nil {
			return contract.ABIParam{}, fmt.Errorf("array of %s: %w", u.Elem(), err)
		}
		ap.ElemParam = &elem
		return ap, nil
	case *types.Slice:
		esrc, epath := spell(pkg, u.Elem())
		ap := contract.ABIParam{
			Go: name, GoType: src, TypePkg: ipath, Kind: contract.ABISlice,
			Model: []string{name + "_ref", name + "_off", name + "_len", name + "_cap"},
			Elem:  esrc, ElemPkg: epath,
		}
		if ew, esigned, ok := intWidth(u.Elem()); ok {
			ap.ElemWidth, ap.ElemSigned = ew, esigned
			return ap, nil
		}
		elem, err := ab.param("elem", u.Elem(), stack)
		if err != nil {
			return contract.ABIParam{}, fmt.Errorf("slice of %s: %w", u.Elem(), err)
		}
		ap.ElemParam = &elem
		return ap, nil
	case *types.Struct:
		// An anonymous struct (or a named one of an opaque kind handled
		// above): described inline.
		fields, err := ab.fields(u, ipath != "", stack)
		if err != nil {
			return contract.ABIParam{}, err
		}
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIStruct, Fields: fields}, nil
	case *types.Pointer:
		pointee, err := ab.param(name, u.Elem(), stack)
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
