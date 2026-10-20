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
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/ir"
	"github.com/andreasfoo/go2lean/internal/ssaout"
)

const Frontend = "gossa"

var specRe = regexp.MustCompile(`go2lean:spec\s+([\w.]+)`)

// maxLenRe is a spec's `go2lean:maxlen N`: its property searches inputs
// up to N long instead of the run's -maxlen.
var maxLenRe = regexp.MustCompile(`go2lean:maxlen\s+(\d+)`)

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
	zeros := zeroGlobals(sprog, spkgs, opts.Deps)

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
		deps := depClosure(roots, mainPaths, zeros, invokedNames(sprog))
		var paths []string
		for path := range deps {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			fns := deps[path]
			sort.Slice(fns, func(i, j int) bool { return funcName(fns[i]) < funcName(fns[j]) })
			if mainPaths[path] {
				// Generic instances of a compiled package: after its own
				// functions, so their FuncIds do not move.
				for k := range units {
					if units[k].path == path {
						units[k].fns = append(units[k].fns, fns...)
					}
				}
				continue
			}
			units = append(units, unit{path, fns})
		}
	}
	// Anonymous functions after the rest, so calls through closures can
	// follow them; their FuncIds come last and move nothing.
	for k := range units {
		units[k].fns = withAnon(units[k].fns)
	}
	// The types modelled code boxes into interfaces: the dynamic types an
	// interface value the model built can have.
	var boxed []types.Type
	seenBoxed := map[string]bool{}
	for _, u := range units {
		for _, f := range u.fns {
			for _, b := range f.Blocks {
				for _, in := range b.Instrs {
					if mi, ok := in.(*ssa.MakeInterface); ok {
						if k := types.TypeString(mi.X.Type(), nil); !seenBoxed[k] {
							seenBoxed[k] = true
							boxed = append(boxed, mi.X.Type())
						}
					}
				}
			}
		}
	}
	for _, u := range units {
		for id, f := range u.fns {
			xrefs[f.String()] = contract.XCall{Package: u.path, ID: id}
		}
	}
	var progs []*ir.Prog
	for _, u := range units {
		p, err := translatePackage(u.path, u.fns, xrefs, zeros, sprog, boxed)
		if err != nil {
			return nil, "", err
		}
		p.Dependency = !mainPaths[u.path]
		if !p.Dependency {
			addCtorWrappers(p, u.fns)
		}
		progs = append(progs, p)
	}
	return progs, modPath, nil
}

// depClosure walks the static calls of roots into packages outside the
// compile and the standard library, up to depDepth calls deep and depLimit
// functions, and returns the functions reached by package path. Besides
// static calls it follows functions used as values, the function a
// package-level variable holds from its initializer (zeros), and the bodies
// of closures (added to their package by withAnon). Generic functions and
// wrappers are left out (they stay unmodelled).
func depClosure(roots []*ssa.Function, mainPaths map[string]bool, zeros map[*ssa.Global]ssa.Value, invoked map[string]bool) map[string][]*ssa.Function {
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
				var refs []*ssa.Function
				for _, op := range in.Operands(nil) {
					switch v := (*op).(type) {
					case *ssa.Function:
						refs = append(refs, v)
					case *ssa.Global:
						if fn, ok := zeros[v].(*ssa.Function); ok {
							refs = append(refs, fn)
						}
					}
				}
				// A boxed value: the methods of its type some call on an
				// interface may reach.
				if mi, ok := in.(*ssa.MakeInterface); ok {
					ms := f.Prog.MethodSets.MethodSet(mi.X.Type())
					for i := 0; i < ms.Len(); i++ {
						if invoked[ms.At(i).Obj().Name()] {
							if fn := f.Prog.MethodValue(ms.At(i)); fn != nil {
								refs = append(refs, fn)
							}
						}
					}
				}
				for _, callee := range refs {
					if callee.Parent() != nil {
						// A closure: walk its body as part of its parent's.
						if !seen[callee] {
							seen[callee] = true
							visit(callee, depth)
						}
						continue
					}
					// Instances of generic functions (SystemMessage[string]) have
					// bodies with concrete types under InstantiateGenerics; their
					// package is that of the generic origin.
					instance := len(callee.TypeArgs()) > 0 && callee.Origin() != nil
					if seen[callee] || (callee.Synthetic != "" && !instance) ||
						callee.Parent() != nil || (callee.TypeParams().Len() > 0 && !instance) ||
						isInit(callee.Name()) || len(callee.Blocks) == 0 {
						continue
					}
					pkg := callee.Pkg
					if instance {
						pkg = callee.Origin().Pkg
					}
					if pkg == nil {
						continue
					}
					path := pkg.Pkg.Path()
					// A compiled package's own functions are modelled already,
					// except instances of its generic functions.
					// The generic helpers of package slices (ContainsFunc,
					// IndexFunc) are plain loops over func values: modelled.
					if (mainPaths[path] && !instance) || (isStdlib(path) && !(instance && path == "slices")) {
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
	}
	for _, f := range roots {
		visit(f, 1)
	}
	return out
}

// zeroGlobals returns the package-level variables whose value a load can
// know: a variable no code writes holds its zero value (nil in the map), one
// the package initializer sets to a constant or a function and nothing else
// writes holds that value. Writes from test helpers (functions named
// ...ForTest or ...ForTesting, and their closures) do not count. With bodies
// for every package (deps) that covers every variable; otherwise only the
// unexported ones of the compiled packages, which no other package can
// reach.
func zeroGlobals(sprog *ssa.Program, spkgs []*ssa.Package, deps bool) map[*ssa.Global]ssa.Value {
	touched := map[*ssa.Global]bool{}
	inits := map[*ssa.Global]ssa.Value{}
	for f := range ssautil.AllFunctions(sprog) {
		if testHelper(f) {
			continue
		}
		pkgInit := f.Name() == "init" && f.Synthetic != ""
		for _, b := range f.Blocks {
			for _, in := range b.Instrs {
				if u, ok := in.(*ssa.UnOp); ok && u.Op == token.MUL {
					continue
				}
				if st, ok := in.(*ssa.Store); ok && pkgInit {
					if g, ok := st.Addr.(*ssa.Global); ok && g.Pkg == f.Pkg {
						switch st.Val.(type) {
						case *ssa.Const, *ssa.Function:
							if _, twice := inits[g]; twice {
								touched[g] = true
							}
							inits[g] = st.Val
							continue
						}
					}
				}
				for _, op := range in.Operands(nil) {
					if g, ok := (*op).(*ssa.Global); ok {
						touched[g] = true
					}
				}
			}
		}
	}
	compiled := map[*ssa.Package]bool{}
	for _, p := range spkgs {
		if p != nil {
			compiled[p] = true
		}
	}
	out := map[*ssa.Global]ssa.Value{}
	for _, p := range sprog.AllPackages() {
		if !deps && !compiled[p] {
			continue
		}
		for _, m := range p.Members {
			g, ok := m.(*ssa.Global)
			if !ok || touched[g] || (!deps && g.Object() != nil && g.Object().Exported()) {
				continue
			}
			out[g] = inits[g]
		}
	}
	return out
}

// testHelper says whether f exists for tests only: named ...ForTest or
// ...ForTesting, or a closure inside one.
func testHelper(f *ssa.Function) bool {
	for f.Parent() != nil {
		f = f.Parent()
	}
	return strings.HasSuffix(f.Name(), "ForTest") || strings.HasSuffix(f.Name(), "ForTesting")
}

// addCtorWrappers adds, for each method of a type T whose package has a
// constructor (NewT or newT returning T or *T, possibly with an error), a
// function T.M~NewT that builds the receiver with the constructor and calls
// the method on it. The method is then targeted through the wrapper: a
// receiver no constructor made (nil maps, nil collaborators) is a misuse,
// not a bug. Wrappers go after every other function, so no FuncId moves.
func addCtorWrappers(p *ir.Prog, fns []*ssa.Function) {
	ids := map[*ssa.Function]int{}
	ctors := map[*types.TypeName]*ssa.Function{}
	for i, f := range fns {
		ids[f] = i
	}
	for _, f := range fns {
		if f.Signature.Recv() != nil || f.Parent() != nil || p.Funcs[ids[f]].Status == contract.ModelRejected {
			continue
		}
		if tn, ok := ctorOf(f); ok {
			if _, dup := ctors[tn]; !dup {
				ctors[tn] = f
			}
		}
	}
	for _, m := range fns {
		recv := m.Signature.Recv()
		if recv == nil || m.Parent() != nil || p.Funcs[ids[m]].Status == contract.ModelRejected || p.Funcs[ids[m]].SpecFor != "" {
			continue
		}
		rt := recv.Type()
		ptrRecv := false
		if pt, ok := rt.(*types.Pointer); ok {
			rt, ptrRecv = pt.Elem(), true
		}
		named, ok := rt.(*types.Named)
		if !ok {
			continue
		}
		ctor, ok := ctors[named.Obj()]
		if !ok || ctor.Signature.Variadic() {
			continue
		}
		_, ctorPtr := ctor.Signature.Results().At(0).Type().(*types.Pointer)
		if ptrRecv && !ctorPtr {
			continue // the constructor's value would need an address
		}
		w, err := ctorWrapper(p, ctor, m, ids[ctor], ids[m], ctorPtr && !ptrRecv)
		if err != nil {
			continue
		}
		w.TestFile = p.Funcs[ids[m]].TestFile || p.Funcs[ids[ctor]].TestFile
		p.Funcs[ids[m]].WrappedBy = w.Name
		p.Funcs = append(p.Funcs, w)
	}
}

// ctorOf reports the type f constructs: a package-level function named
// New<T> or new<T> whose first result is T or *T for a named T of its own
// package, and whose other result, if any, is an error.
func ctorOf(f *ssa.Function) (*types.TypeName, bool) {
	res := f.Signature.Results()
	if res.Len() == 0 || res.Len() > 2 || (res.Len() == 2 && !isError(res.At(1).Type())) {
		return nil, false
	}
	t := res.At(0).Type()
	if pt, ok := t.(*types.Pointer); ok {
		t = pt.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || f.Pkg == nil || named.Obj().Pkg() != f.Pkg.Pkg {
		return nil, false
	}
	n := named.Obj().Name()
	if f.Name() != "New"+n && f.Name() != "new"+n && f.Name() != "New"+strings.ToUpper(n[:1])+n[1:] {
		return nil, false
	}
	return named.Obj(), true
}

// ctorWrapper builds T.M~NewT: call the constructor on the first
// parameters, stop (returning unit) when it reports an error, call the
// method on its result and the remaining parameters. Either call getting
// stuck makes the run stuck (the wrapper exists to be checked, so an
// unmodelled callee must not read as "no panic"). deref loads the receiver
// from the constructor's pointer for a value receiver.
func ctorWrapper(p *ir.Prog, ctor, m *ssa.Function, ctorID, mID int, deref bool) (*ir.Func, error) {
	cabi, err := abiOf(ctor)
	if err != nil {
		return nil, err
	}
	mabi, err := abiOf(m)
	if err != nil {
		return nil, err
	}
	name := funcName(m) + "~" + ctor.Name()
	abi := *mabi
	abi.GoFunc = m.String()
	abi.GoSig = fmt.Sprintf("func%s via %s", strings.TrimPrefix(m.Signature.String(), "func"), ctor.Name())
	abi.Method = ""
	abi.Variadic = m.Signature.Variadic()
	errRes := ctor.Signature.Results().Len() == 2
	abi.Ctor = &contract.ABICtor{Func: ctor.Name(), N: len(cabi.Params), Err: errRes, Method: m.Name()}
	abi.Imports = map[string]string{}
	for k, v := range cabi.Imports {
		abi.Imports[k] = v
	}
	for k, v := range mabi.Imports {
		abi.Imports[k] = v
	}
	seenT := map[string]bool{}
	abi.Types = nil
	for _, t := range append(append([]contract.ABIType{}, cabi.Types...), mabi.Types...) {
		if !seenT[t.GoType] {
			seenT[t.GoType] = true
			abi.Types = append(abi.Types, t)
		}
	}
	// Constructor parameters, then the method's without the receiver; a
	// method parameter named like a constructor one is renamed.
	taken := map[string]bool{}
	abi.Params = nil
	for _, prm := range cabi.Params {
		taken[prm.Go] = true
		abi.Params = append(abi.Params, prm)
	}
	for _, prm := range mabi.Params[1:] {
		for taken[prm.Go] {
			prm.Go += "_"
			prm.Model = append([]string{prm.Go}, prm.Model[1:]...)
		}
		taken[prm.Go] = true
		abi.Params = append(abi.Params, prm)
	}
	nc, nm := len(ctor.Params), len(m.Params)-1
	w := &ir.Func{Name: name, GoSig: abi.GoSig, ABI: abi, Status: contract.ModelComplete, Calls: []int{ctorID, mID}}
	v := 0
	fresh := func() ir.Var { v++; return ir.Var(v - 1) }
	for i := 0; i < nc+nm; i++ {
		w.Params = append(w.Params, fresh())
	}
	r, res, unit := fresh(), fresh(), fresh()
	// Blocks: 0 call ctor; 1 receiver (and error check); 2 call method;
	// 3 return the method's result; 4 return unit; 5 stuck.
	w.Blocks = make([]ir.Block, 6)
	w.Blocks[0] = ir.Block{Name: "ctor", Term: ir.Term{Kind: ir.TermCall, Callee: ctorID, CArgs: w.Params[:nc], Dst: r, Next: 1, Fallback: 5}}
	recv := r
	b1 := ir.Block{Name: "recv"}
	if errRes {
		recv = fresh()
		e, nilErr, isNil := fresh(), fresh(), fresh()
		b1.Instrs = append(b1.Instrs,
			ir.Instr{Dst: recv, Op: ir.Extract{I: 0}, Args: []ir.Var{r}},
			ir.Instr{Dst: e, Op: ir.Extract{I: 1}, Args: []ir.Var{r}},
			ir.Instr{Dst: nilErr, Op: ir.Const{V: ir.ErrVal{}}},
			ir.Instr{Dst: isNil, Op: ir.Simple("eq"), Args: []ir.Var{e, nilErr}})
		b1.Term = ir.Term{Kind: ir.TermBr, Cond: isNil, T: 2, F: 4}
	} else {
		b1.Term = ir.Term{Kind: ir.TermJump, B: 2}
	}
	if deref {
		val := fresh()
		b1.Instrs = append(b1.Instrs, ir.Instr{Dst: val, Op: ir.Simple("loadPath"), Args: []ir.Var{recv}})
		recv = val
	}
	w.Blocks[1] = b1
	w.Blocks[2] = ir.Block{Name: "method", Term: ir.Term{Kind: ir.TermCall, Callee: mID, CArgs: append([]ir.Var{recv}, w.Params[nc:]...), Dst: res, Next: 3, Fallback: 5}}
	w.Blocks[3] = ir.Block{Name: "ret", Term: ir.Term{Kind: ir.TermRet, V: res}}
	w.Blocks[4] = ir.Block{Name: "ctor-error", Instrs: []ir.Instr{{Dst: unit, Op: ir.Const{V: ir.UnitVal{}}}}, Term: ir.Term{Kind: ir.TermRet, V: unit}}
	w.Blocks[5] = ir.Block{Name: "stuck", Term: ir.Term{Kind: ir.TermPanic, Panic: contract.PanicOther}}
	w.NVals = v
	return w, nil
}

// inTestFile says whether f (or the function enclosing a closure) is
// declared in a _test.go file.
func inTestFile(f *ssa.Function) bool {
	for f.Parent() != nil {
		f = f.Parent()
	}
	if f.Prog == nil || !f.Pos().IsValid() {
		return false
	}
	return strings.HasSuffix(f.Prog.Fset.Position(f.Pos()).Filename, "_test.go")
}

// withAnon appends the anonymous functions of fns (and theirs, nested)
// that are not in fns yet.
func withAnon(fns []*ssa.Function) []*ssa.Function {
	seen := map[*ssa.Function]bool{}
	for _, f := range fns {
		seen[f] = true
	}
	out := append([]*ssa.Function(nil), fns...)
	for i := 0; i < len(out); i++ {
		for _, a := range out[i].AnonFuncs {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// invokedNames returns the names of the methods called on interface values
// anywhere in the program.
func invokedNames(sprog *ssa.Program) map[string]bool {
	out := map[string]bool{}
	for f := range ssautil.AllFunctions(sprog) {
		for _, b := range f.Blocks {
			for _, in := range b.Instrs {
				if c, ok := in.(ssa.CallInstruction); ok && c.Common().IsInvoke() {
					out[c.Common().Method.Name()] = true
				}
			}
		}
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

func translatePackage(pkgPath string, fns []*ssa.Function, xrefs map[string]contract.XCall, zeros map[*ssa.Global]ssa.Value, sprog *ssa.Program, boxed []types.Type) (*ir.Prog, error) {
	p := &ir.Prog{Package: pkgPath, Frontend: Frontend}
	st := &progState{prog: p, ids: map[*ssa.Function]int{}, xrefs: xrefs, zeros: zeros, sprog: sprog, boxed: boxed}

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
	// zeros holds the package-level variables nothing writes: a load of
	// one is its zero value.
	zeros map[*ssa.Global]ssa.Value
	sprog *ssa.Program
	// boxed lists the types modelled code boxes into interfaces.
	boxed []types.Type
}

// maxImplementers bounds the boxed types an interface assertion or a
// method call on an interface considers; beyond it they stay unmodelled.
const maxImplementers = 64

// implementers returns the boxed types implementing iface, or false when
// there are more than maxImplementers.
func (s *progState) implementers(iface *types.Interface) ([]types.Type, bool) {
	var out []types.Type
	for _, t := range s.boxed {
		if types.Implements(t, iface) {
			out = append(out, t)
		}
	}
	return out, len(out) <= maxImplementers
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
	if decl, ok := f.Syntax().(*ast.FuncDecl); ok && decl.Doc != nil && t.out.SpecFor != "" {
		if m := maxLenRe.FindStringSubmatch(decl.Doc.Text()); m != nil {
			t.out.SpecMaxLen, _ = strconv.Atoi(m[1])
		}
	}
	if f.Blocks == nil {
		return fmt.Errorf("no body")
	}
	// A closure receives its captured values before its parameters, as
	// funcRef stores them.
	t.out.Closure = f.Parent() != nil
	t.out.TestFile = inTestFile(f)
	for _, fv := range f.FreeVars {
		v := t.fresh()
		t.vars[fv] = v
		t.out.Params = append(t.out.Params, v)
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
	markLastExit(t.out.Exits)
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
	switch fn := x.(type) {
	case *ssa.Function:
		// A function used as a value: a reference a call through it follows
		// when the function is modelled. Not cached, like a constant: the
		// block that first used it need not dominate the next use.
		v := t.fresh()
		return v, t.funcValue(v, fn, nil)
	case *ssa.Builtin:
		v := t.fresh()
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

// mapKey records a string constant used as a map key (m["role"]): the
// generator builds input maps mostly from such keys.
func (t *fnState) mapKey(k ssa.Value) {
	c, ok := k.(*ssa.Const)
	if !ok || c.Value == nil || c.Value.Kind() != constant.String {
		return
	}
	if s := constant.StringVal(c.Value); !slices.Contains(t.out.MapKeys, s) {
		t.out.MapKeys = append(t.out.MapKeys, s)
	}
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
	if (x.Op == token.EQL || x.Op == token.NEQ) && isString(x.X.Type()) {
		// Strings compared against a constant: candidate JSON keys and values.
		for _, o := range []ssa.Value{x.X, x.Y} {
			if c, ok := o.(*ssa.Const); ok && c.Value != nil && c.Value.Kind() == constant.String {
				if s := constant.StringVal(c.Value); !slices.Contains(t.out.CmpStrs, s) {
					t.out.CmpStrs = append(t.out.CmpStrs, s)
				}
			}
		}
	}
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
	} else if !isInt && !(eqOp && (isBool(x.X.Type()) || isError(x.X.Type()) || isPointer(x.X.Type()) || isSlice(x.X.Type()) || flatComparable(x.X.Type()))) {
		return fmt.Errorf("binary operator on unsupported type %s", x.X.Type())
	}
	if eqOp && flatComparable(x.X.Type()) {
		op = pick(x.Op == token.EQL, "eqDeep", "neDeep")
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

// flatComparable says whether t is an array or struct whose elements, all
// the way down, are integers, bools, pointers or strings: == on it compares
// element by element (eqDeep: strings by their bytes).
func flatComparable(t types.Type) bool {
	var leaf func(t types.Type) bool
	leaf = func(t types.Type) bool {
		if _, _, ok := intWidth(t); ok {
			return true
		}
		if isBool(t) || isPointer(t) || isString(t) {
			return true
		}
		switch u := t.Underlying().(type) {
		case *types.Array:
			return leaf(u.Elem())
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				if !leaf(u.Field(i).Type()) {
					return false
				}
			}
			return true
		}
		return false
	}
	switch t.Underlying().(type) {
	case *types.Array, *types.Struct:
		return leaf(t)
	}
	return false
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
	// A func, map, channel or interface the model knows nothing about:
	// unknown (every use of it is havoc), not an assumption.
	if isNilable(ty) {
		t.emit(ir.Instr{Dst: dst, Op: ir.Const{V: ir.Opaque{}}})
		return nil
	}
	// A struct, or a pointer to a fresh one: havoc field by field, so the
	// engines can vary its contents (see structHavoc).
	if st, ok := ty.Underlying().(*types.Struct); ok {
		if t.structHavoc(dst, st, site, reason, goHint, 3) {
			return nil
		}
	}
	if pt, ok := ty.Underlying().(*types.Pointer); ok {
		if st, ok := pt.Elem().Underlying().(*types.Struct); ok {
			if zero, ok := zeroVal(pt.Elem()); ok {
				val := t.fresh()
				if t.structHavoc(val, st, site, reason, goHint, 3) {
					t.issue("%s: assumed a pointer to a fresh %s with havoc fields (not modelled)", site, pt.Elem())
					t.emit(ir.Instr{Dst: dst, Op: ir.Alloc{Zero: zero}})
					t.emit(ir.Instr{Dst: t.fresh(), Op: ir.Simple("storePath"), Args: []ir.Var{dst, val}})
					return nil
				}
			}
		}
	}
	// What is left is a fixed guess the engines cannot vary: logged when it
	// runs (a marker havoc read), except for byte slices of havoc length.
	marker := func() {
		t.emit(ir.Instr{Dst: t.fresh(), HavocSite: t.prog.newHavoc(ir.HavocAssumption, site+" (assumed)", "assumed", goHint)})
	}
	switch u := ty.Underlying().(type) {
	case *types.Pointer:
		if zero, ok := zeroVal(u.Elem()); ok {
			marker()
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
			if w, _, isInt := intWidth(u.Elem()); !isInt || w != 8 {
				marker()
			}
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
	marker()
	t.issue("%s: assumed the zero value of %s (not modelled)", site, ty)
	t.emit(ir.Instr{Dst: dst, Op: ir.Const{V: v}})
	return nil
}

// structHavoc binds dst to a struct value of type st whose fields are
// havoc where they can be (integers, bools, strings, errors), unknown for
// funcs, maps, channels and interfaces, havoc'd recursively for nested
// structs down to depth, and zero otherwise (nil pointers and slices).
// It reports false when some field has no zero value.
func (t *fnState) structHavoc(dst ir.Var, st *types.Struct, site, reason, goHint string, depth int) bool {
	var fields []ir.Var
	for i := 0; i < st.NumFields(); i++ {
		ft := st.Field(i).Type()
		v := t.fresh()
		fsite := site + "." + st.Field(i).Name()
		switch {
		case func() bool { _, ok := havocKind(ft); return ok }():
			k, _ := havocKind(ft)
			t.emit(ir.Instr{Dst: v, HavocSite: t.prog.newHavoc(k, fsite, reason, goHint)})
		case isNilable(ft):
			t.emit(ir.Instr{Dst: v, Op: ir.Const{V: ir.Opaque{}}})
		default:
			if inner, ok := ft.Underlying().(*types.Struct); ok && depth > 0 && !isStringsBuilder(ft) {
				if t.structHavoc(v, inner, fsite, reason, goHint, depth-1) {
					break
				}
			}
			zero, ok := zeroVal(ft)
			if !ok {
				return false
			}
			t.emit(ir.Instr{Dst: v, Op: ir.Const{V: zero}})
		}
		fields = append(fields, v)
	}
	t.emit(ir.Instr{Dst: dst, Op: ir.Simple("mkStruct"), Args: fields})
	return true
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
		// A map the program builds: a list of key/value pairs on the heap.
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("makeMap")})
		return nil
	case *ssa.MakeClosure:
		// A closure is a func value holding the function and the values it
		// captured (free variables are pointers to the shared cells).
		fn, _ := x.Fn.(*ssa.Function)
		if fn == nil {
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.OpaqueNonNil{}}})
			return nil
		}
		return t.funcValue(t.def(x), fn, x.Bindings)
	case *ssa.MakeChan:
		t.issue("channel %s is opaque (not modelled)", x.Name())
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.OpaqueNonNil{}}})
		return nil
	case *ssa.MapUpdate:
		t.mapKey(x.Key)
		m, err := t.val(x.Map)
		if err != nil {
			return err
		}
		k, err := t.val(x.Key)
		if err != nil {
			return err
		}
		v, err := t.val(x.Value)
		if err != nil {
			return err
		}
		// A map the model knows nothing about (an input field, a havoc
		// value): the update is ignored, as before.
		return t.onKnown(m, func() error {
			t.emit(ir.Instr{Dst: t.fresh(), Op: ir.Simple("mapUpdate"), Args: []ir.Var{m, k, v}})
			return nil
		}, func() error {
			t.issue("map update %s[%s] on a map the model knows nothing about ignored", x.Map.Name(), x.Key.Name())
			return nil
		})
	case *ssa.Defer:
		// A deferred call to a function in the model would need the defer
		// stack; one the model does not look into (mutex unlocks, closes,
		// closures) is an effect it does not model anyway.
		if callee := x.Call.StaticCallee(); callee != nil && callee.Parent() == nil {
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
			// Insertion order; a map the model knows nothing about has no
			// entries.
			t.issue("range over map %s: in insertion order; a map the model knows nothing about has no entries", x.X.Name())
			m, err := t.val(x.X)
			if err != nil {
				return err
			}
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Simple("mapRange"), Args: []ir.Var{m}})
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
		// A map iterator: (ok, key, value), zeros when done.
		tu := x.Type().(*types.Tuple)
		zeros := make([]ir.Val, 2)
		for i := 1; i < tu.Len() && i < 3; i++ {
			z, ok := zeroVal(tu.At(i).Type())
			if !ok {
				if z, ok = assumedValue(tu.At(i).Type()); !ok {
					z = ir.Opaque{} // an unused component has the invalid type
				}
			}
			zeros[i-1] = z
		}
		for i := range zeros {
			if zeros[i] == nil {
				zeros[i] = ir.Opaque{}
			}
		}
		t.emit(ir.Instr{Dst: t.def(x), Op: ir.MapNext{ZeroK: zeros[0], ZeroV: zeros[1]}, Args: []ir.Var{it}})
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
		if iface, ok := x.AssertedType.Underlying().(*types.Interface); ok {
			if impls, ok := t.prog.implementers(iface); ok {
				return t.ifaceAssert(x, impls)
			}
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
		if mt, isMap := x.X.Type().Underlying().(*types.Map); isMap {
			t.mapKey(x.Index)
			zero, ok := zeroVal(mt.Elem())
			if !ok {
				zero, ok = assumedValue(mt.Elem())
			}
			if !ok {
				return t.havoc(x, "map lookup", "opaque-map", "")
			}
			m, err := t.val(x.X)
			if err != nil {
				return err
			}
			k, err := t.val(x.Index)
			if err != nil {
				return err
			}
			dst := t.def(x)
			return t.onKnown(m, func() error {
				t.emit(ir.Instr{Dst: dst, Op: ir.MapLookup{CommaOk: x.CommaOk, Zero: zero}, Args: []ir.Var{m, k}})
				return nil
			}, func() error {
				t.issue("lookup in map %s the model knows nothing about is havoc", x.X.Name())
				return t.havocResult(dst, x, "map lookup", "opaque-map")
			})
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
		if g, isGlobal := x.X.(*ssa.Global); isGlobal {
			if init, known := t.prog.zeros[g]; known {
				if init != nil {
					v, err := t.val(init)
					if err != nil {
						return err
					}
					t.vars[x] = v
					return nil
				}
				if zero, ok := zeroVal(x.Type()); ok {
					t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: zero}})
					return nil
				}
			}
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
			if _, isMap := x.Call.Args[0].Type().Underlying().(*types.Map); isMap && bi.Name() == "len" {
				m, err := t.val(x.Call.Args[0])
				if err != nil {
					return err
				}
				dst := t.def(x)
				return t.onKnown(m, func() error {
					t.emit(ir.Instr{Dst: dst, Op: ir.Simple("mapLen"), Args: []ir.Var{m}})
					return nil
				}, func() error {
					t.opaqueLen(dst, bi.Name(), x.Call.Args[0].Type())
					return nil
				})
			}
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
		case "delete":
			if _, isMap := x.Call.Args[0].Type().Underlying().(*types.Map); isMap {
				m, err := t.val(x.Call.Args[0])
				if err != nil {
					return err
				}
				k, err := t.val(x.Call.Args[1])
				if err != nil {
					return err
				}
				t.def(x)
				return t.onKnown(m, func() error {
					t.emit(ir.Instr{Dst: t.fresh(), Op: ir.Simple("mapDelete"), Args: []ir.Var{m, k}})
					return nil
				}, func() error { return nil })
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
				t.opaqueLen(t.def(x), bi.Name(), x.Call.Args[0].Type())
				return nil
			}
			return t.havoc(x, "builtin "+bi.Name(), "unmodeled-call", "")
		}
		t.issue("call to builtin %s ignored", bi.Name())
		return nil
	}
	callee := x.Call.StaticCallee()
	if callee != nil && callee.Name() == "go2leanProbe" && len(x.Call.Args) == 2 {
		// A spec's probe: record the value, run nothing.
		if c, ok := x.Call.Args[0].(*ssa.Const); ok && c.Value != nil && c.Value.Kind() == constant.String {
			v, err := t.val(x.Call.Args[1])
			if err != nil {
				return err
			}
			t.emit(ir.Instr{Probe: constant.StringVal(c.Value), Args: []ir.Var{v}})
			t.emit(ir.Instr{Dst: t.def(x), Op: ir.Const{V: ir.UnitVal{}}})
			return nil
		}
	}
	if callee != nil && callee.String() == "fmt.Sprintf" && len(x.Call.Args) == 2 {
		return t.sprintf(x)
	}
	if callee != nil && callee.String() == "encoding/json.Unmarshal" && len(x.Call.Args) == 2 {
		if done, err := t.jsonUnmarshal(x); done || err != nil {
			return err
		}
	}
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
	if x.Call.IsInvoke() {
		if done, err := t.invoke(x); done || err != nil {
			return err
		}
	}
	if callee == nil && !x.Call.IsInvoke() {
		// A call through a func value: the model follows the function it
		// names, if it knows it.
		fv, err := t.val(x.Call.Value)
		if err != nil {
			return err
		}
		return t.emitCall(x, ir.Term{Kind: ir.TermCallV, FV: fv}, x.Call.Value.Name())
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
// markLastExit flags the return statement with the highest source line.
func markLastExit(exits []contract.ExitSite) {
	last := -1
	for i, e := range exits {
		if last < 0 || e.Line > exits[last].Line {
			last = i
		}
	}
	if last >= 0 {
		exits[last].Last = true
	}
}

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
	h.Write([]byte(tagName(t)))
	return h.Sum32()
}

var anyAliasRe = regexp.MustCompile(`\bany\b`)

// tagName spells t for its tag the same way whether the source wrote the
// alias any or interface{}: they are one type at run time.
func tagName(t types.Type) string {
	return anyAliasRe.ReplaceAllString(types.TypeString(t, nil), "interface{}")
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

// sprintf lowers fmt.Sprintf: the formatted string when the model can
// format it (plain %s %d %v %t %% on strings, integers and bools the
// program boxed), else the havoc string an unmodelled call gives.
// jsonUnmarshal lowers json.Unmarshal(data, &v) for v of a JSON-shaped
// type (string, bool, interface{}, map[string]interface{}, []interface{}):
// the model parses the bytes. Other targets (structs) stay unmodelled.
func (t *fnState) jsonUnmarshal(x *ssa.Call) (bool, error) {
	mi, ok := x.Call.Args[1].(*ssa.MakeInterface)
	if !ok {
		return false, nil
	}
	pt, ok := mi.X.Type().Underlying().(*types.Pointer)
	if !ok {
		return false, nil
	}
	kind := -1
	elem := pt.Elem()
	switch u := elem.Underlying().(type) {
	case *types.Basic:
		if isString(elem) {
			kind = 0
		} else if isBool(elem) {
			kind = 1
		}
	case *types.Interface:
		if isEmptyInterface(elem) {
			kind = 2
		}
	case *types.Map:
		if isString(u.Key()) && isEmptyInterface(u.Elem()) {
			kind = 3
		}
	case *types.Slice:
		if isEmptyInterface(u.Elem()) {
			kind = 4
		}
	}
	if kind < 0 {
		return false, nil
	}
	data, err := t.val(x.Call.Args[0])
	if err != nil {
		return true, err
	}
	p, err := t.val(mi.X)
	if err != nil {
		return true, err
	}
	empty := types.NewInterfaceType(nil, nil)
	op := ir.JSONUnmarshal{Kind: kind, Tags: [5]uint32{
		typeTag(types.Typ[types.String]), typeTag(types.Typ[types.Bool]), typeTag(types.Typ[types.Float64]),
		typeTag(types.NewMap(types.Typ[types.String], empty)), typeTag(types.NewSlice(empty)),
	}}
	t.emit(ir.Instr{Dst: t.def(x), Op: op, Args: []ir.Var{data, p}})
	t.out.UsesJSON = true
	return true, nil
}

func (t *fnState) sprintf(x *ssa.Call) error {
	f, err := t.val(x.Call.Args[0])
	if err != nil {
		return err
	}
	args, err := t.val(x.Call.Args[1])
	if err != nil {
		return err
	}
	dst := t.def(x)
	ok := t.fresh()
	t.emit(ir.Instr{Dst: ok, Op: ir.Simple("sprintfOk"), Args: []ir.Var{f, args}})
	yes := len(t.out.Blocks)
	no, join := yes+1, yes+2
	t.cur.Term = ir.Term{Kind: ir.TermBr, Cond: ok, T: yes, F: no}
	name := t.cur.Name
	t.out.Blocks = append(t.out.Blocks,
		ir.Block{Name: name + ".sprintf"},
		ir.Block{Name: name + ".sprintf-unknown"},
		ir.Block{Name: name + ".sprintf-join"})
	t.cur = &t.out.Blocks[yes]
	t.emit(ir.Instr{Dst: dst, Op: ir.Simple("sprintf"), Args: []ir.Var{f, args}})
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: join}
	t.cur = &t.out.Blocks[no]
	if err := t.havocInto(dst, x.Type(), t.f.Name()+": call fmt.Sprintf", "unmodeled-call", ""); err != nil {
		return err
	}
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: join}
	t.cur = &t.out.Blocks[join]
	return nil
}

// ifaceAssert lowers x.(I) for an interface I: a value the model boxed
// passes when its type is among impls, the boxed types implementing I.
func (t *fnState) ifaceAssert(x *ssa.TypeAssert, impls []types.Type) error {
	v, err := t.val(x.X)
	if err != nil {
		return err
	}
	tags := make([]uint32, len(impls))
	for i, ty := range impls {
		tags[i] = typeTag(ty)
	}
	dst := t.def(x)
	return t.onKnown(v, func() error {
		t.emit(ir.Instr{Dst: dst, Op: ir.AssertIface{Tags: tags, CommaOk: x.CommaOk}, Args: []ir.Var{v}})
		return nil
	}, func() error {
		t.issue("type assertion %s on an interface value the model did not build is havoc", x.Name())
		return t.havocResult(dst, x, "type assertion", "opaque-assert")
	})
}

// invoke lowers a method call on an interface value: the method of the
// boxed value's type, among the boxed types implementing the interface
// whose method the model holds; a nil interface panics, any other value
// makes the call unmodelled. It reports false when no type qualifies.
func (t *fnState) invoke(x *ssa.Call) (bool, error) {
	iface, ok := x.Call.Value.Type().Underlying().(*types.Interface)
	if !ok {
		return false, nil
	}
	impls, ok := t.prog.implementers(iface)
	if !ok {
		return false, nil
	}
	m := x.Call.Method
	var tags []uint32
	var fns []*ssa.Function
	for _, ty := range impls {
		sel := t.prog.sprog.MethodSets.MethodSet(ty).Lookup(m.Pkg(), m.Name())
		if sel == nil {
			continue
		}
		fn := t.prog.sprog.MethodValue(sel)
		if fn == nil {
			continue
		}
		if _, ok := t.prog.ids[fn]; !ok {
			if xr, ok := t.prog.xrefs[fn.String()]; !ok || xr.Package == t.prog.prog.Package {
				continue
			}
		}
		tags = append(tags, typeTag(ty))
		fns = append(fns, fn)
	}
	if len(fns) == 0 {
		return false, nil
	}
	recv, err := t.val(x.Call.Value)
	if err != nil {
		return false, err
	}
	args := []ir.Var{recv}
	for _, fn := range fns {
		v := t.fresh()
		if err := t.funcValue(v, fn, nil); err != nil {
			return false, err
		}
		args = append(args, v)
	}
	fv := t.fresh()
	t.emit(ir.Instr{Dst: fv, Op: ir.Dispatch{Tags: tags}, Args: args})
	return true, t.emitCall(x, ir.Term{Kind: ir.TermCallV, FV: fv}, x.Call.Method.Name())
}

// opaqueLen binds dst to the length of a map or channel the model does
// not look into: havoc, but kept below 16 so that `make([]T, 0, len(m))`
// stays something the model materialises. Recorded as an assumption.
func (t *fnState) opaqueLen(dst ir.Var, name string, ty types.Type) {
	t.issue("%s of %s assumed below 16 (not modelled)", name, ty)
	raw := t.fresh()
	t.emit(ir.Instr{Dst: raw, HavocSite: t.prog.newHavoc(64, fmt.Sprintf("%s: %s", t.f.Name(), name), "unmodeled-call", "")})
	sixteen := t.fresh()
	t.emit(ir.Instr{Dst: sixteen, Op: ir.Const{V: ir.IntVal{Width: 64, Bits: "16"}}})
	t.emit(ir.Instr{Dst: dst, Op: ir.Simple("urem"), Args: []ir.Var{raw, sixteen}})
}

// onKnown runs known in a block taken when the func, map, channel or
// interface value v is one the model knows (nil, or built by the program)
// and unknown otherwise; both continue in a join block.
func (t *fnState) onKnown(v ir.Var, known, unknown func() error) error {
	isKnown := t.fresh()
	t.emit(ir.Instr{Dst: isKnown, Op: ir.Simple("nilKnown"), Args: []ir.Var{v}})
	yes := len(t.out.Blocks)
	no, join := yes+1, yes+2
	t.cur.Term = ir.Term{Kind: ir.TermBr, Cond: isKnown, T: yes, F: no}
	name := t.cur.Name
	t.out.Blocks = append(t.out.Blocks,
		ir.Block{Name: name + ".known"},
		ir.Block{Name: name + ".unknown"},
		ir.Block{Name: name + ".known-join"})
	t.cur = &t.out.Blocks[yes]
	if err := known(); err != nil {
		return err
	}
	t.cur.Term = ir.Term{Kind: ir.TermJump, B: join}
	t.cur = &t.out.Blocks[no]
	if err := unknown(); err != nil {
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
	callArgs := x.Call.Args
	if mc, ok := x.Call.Value.(*ssa.MakeClosure); ok && term.Kind != ir.TermCallV {
		// A closure called directly: its captured values come first.
		callArgs = append(append([]ssa.Value(nil), mc.Bindings...), callArgs...)
	}
	for _, a := range callArgs {
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

// funcValue binds dst to the func value of fn capturing bindings: a
// reference into the model when fn is modelled, here or in a sibling
// package, else a func value the model knows nothing about (non-nil).
func (t *fnState) funcValue(dst ir.Var, fn *ssa.Function, bindings []ssa.Value) error {
	var args []ir.Var
	for _, b := range bindings {
		v, err := t.val(b)
		if err != nil {
			return err
		}
		args = append(args, v)
	}
	if id, ok := t.prog.ids[fn]; ok {
		if !slices.Contains(t.out.Calls, id) {
			t.out.Calls = append(t.out.Calls, id)
		}
		t.emit(ir.Instr{Dst: dst, Op: ir.FuncRef{ID: id}, Args: args})
		return nil
	}
	if xr, ok := t.prog.xrefs[fn.String()]; ok && xr.Package != t.prog.prog.Package {
		if !slices.Contains(t.out.XCalls, xr) {
			t.out.XCalls = append(t.out.XCalls, xr)
		}
		t.emit(ir.Instr{Dst: dst, Op: ir.XFuncRef{Key: ssaout.RegistryKey(Frontend, xr.Package), ID: xr.ID}, Args: args})
		return nil
	}
	t.issue("func value %s is opaque (not modelled)", fn.Name())
	t.emit(ir.Instr{Dst: dst, Op: ir.Const{V: ir.OpaqueNonNil{}}})
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

// isHandle says whether t is a struct type of another package, outside
// the standard library, whose package exports a function returning *t (a
// constructor: gorm.Open, NewStore). Pointers to it are handles the
// constructor builds, not data a caller fills in.
func isHandle(pkg *types.Package, t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg() == pkg || isStdlib(n.Obj().Pkg().Path()) {
		return false
	}
	if _, ok := n.Underlying().(*types.Struct); !ok {
		return false
	}
	if v, ok := handleCache[n]; ok {
		return v
	}
	scope := n.Obj().Pkg().Scope()
	found := false
	for _, name := range scope.Names() {
		fn, ok := scope.Lookup(name).(*types.Func)
		if !ok || !fn.Exported() {
			continue
		}
		res := fn.Type().(*types.Signature).Results()
		if res.Len() == 0 {
			continue
		}
		if pt, ok := res.At(0).Type().(*types.Pointer); ok && types.Identical(pt.Elem(), n) {
			found = true
			break
		}
	}
	handleCache[n] = found
	return found
}

var handleCache = map[*types.Named]bool{}

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

// funcPkg is the package a function belongs to; an instance of a generic
// function has none of its own and belongs to its origin's.
func funcPkg(f *ssa.Function) *types.Package {
	if f.Pkg == nil && f.Origin() != nil && f.Origin().Pkg != nil {
		return f.Origin().Pkg.Pkg
	}
	if f.Pkg == nil {
		return nil
	}
	return f.Pkg.Pkg
}

func abiOf(f *ssa.Function) (*contract.ABI, error) {
	abi := &contract.ABI{GoFunc: f.String(), GoSig: f.Signature.String(), Frontend: Frontend, Package: funcPkg(f).Path(), Variadic: f.Signature.Variadic(), Imports: map[string]string{}}
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
	pkg := funcPkg(f)
	ab := &abiBuilder{pkg: pkg, abi: abi, index: map[*types.Named]int{}, fset: f.Prog.Fset}
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
	// anyDepth counts the empty interfaces being described around the
	// current value: an interface inside an interface's map or slice holds
	// scalars only, so the description stays finite.
	anyDepth int
	fset     *token.FileSet
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
	meta := isRespJSONMeta(u)
	for i := 0; i < u.NumFields(); i++ {
		fld := u.Field(i)
		if foreign && meta && fld.Name() == "raw" {
			// The raw JSON behind RawJSON(): a JSON text of extra fields.
			fields = append(fields, contract.ABIParam{Go: "raw", GoType: "string", Model: []string{"raw"}, Kind: contract.ABIString, JSONText: true})
			continue
		}
		if foreign && meta && fld.Name() == "ExtraFields" {
			// Decoding sets it from the raw JSON; a test cannot set it on
			// its own (the metadata struct cannot be spelled), so it stays
			// nil in the model.
			fields = append(fields, contract.ABIParam{Go: fld.Name(), GoType: types.TypeString(fld.Type(), nil), Model: []string{fld.Name()}, Kind: contract.ABIOpaque, Zero: "nil", ZeroModel: map[string]any{"k": "nil"}})
			continue
		}
		if foreign && !fld.Exported() {
			p := contract.ABIParam{Go: fld.Name(), GoType: types.TypeString(fld.Type(), nil), Model: []string{fld.Name()}, Kind: contract.ABIOpaque, Zero: zeroLit(fld.Type(), types.TypeString(fld.Type(), nil))}
			if z, ok := zeroVal(fld.Type()); ok {
				p.ZeroModel = zeroModel(z)
			}
			fields = append(fields, p)
			continue
		}
		f, err := ab.param(fld.Name(), fld.Type(), stack)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", fld.Name(), err)
		}
		if f.Kind == contract.ABIString && len(f.Enum) == 0 && ab.fset != nil {
			f.Enum = docEnum(ab.fset, fld)
		}
		f.JSONName = jsonName(fld, u.Tag(i))
		fields = append(fields, f)
	}
	return fields, nil
}

var (
	docLines   = map[string][]string{}
	anyOfRe    = regexp.MustCompile(`^\s*//\s*Any of (.*)$`)
	quotedRe   = regexp.MustCompile(`"([^"\\]*)"`)
	docLinesMu sync.Mutex
)

// docEnum reads the values a string field documents as `// Any of "a",
// "b".` in the comment above it (the convention of the Stainless-generated
// OpenAI and Anthropic SDKs, whose enum-like fields are plain strings).
func docEnum(fset *token.FileSet, fld *types.Var) []string {
	pos := fset.Position(fld.Pos())
	if pos.Filename == "" || pos.Line < 2 {
		return nil
	}
	docLinesMu.Lock()
	lines, ok := docLines[pos.Filename]
	if !ok {
		if b, err := os.ReadFile(pos.Filename); err == nil {
			lines = strings.Split(string(b), "\n")
		}
		docLines[pos.Filename] = lines
	}
	docLinesMu.Unlock()
	for i := pos.Line - 2; i >= 0 && i < len(lines); i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "//") {
			break
		}
		if m := anyOfRe.FindStringSubmatch(l); m != nil {
			var out []string
			for _, q := range quotedRe.FindAllStringSubmatch(m[1], -1) {
				out = append(out, q[1])
			}
			return out
		}
	}
	return nil
}

// jsonName is the key encoding/json uses for a struct field.
func jsonName(fld *types.Var, tag string) string {
	name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
	if name == "" {
		return fld.Name()
	}
	return name
}

// isRespJSONMeta says whether u is the JSON metadata struct of an OpenAI or
// Anthropic SDK response type: fields of type respjson.Field (and
// ExtraFields) next to an unexported raw string.
func isRespJSONMeta(u *types.Struct) bool {
	hasRaw, hasField := false, false
	for i := 0; i < u.NumFields(); i++ {
		f := u.Field(i)
		if f.Name() == "raw" && isString(f.Type()) {
			hasRaw = true
		}
		if n, ok := types.Unalias(f.Type()).(*types.Named); ok && n.Obj().Name() == "Field" && n.Obj().Pkg() != nil &&
			strings.HasSuffix(n.Obj().Pkg().Path(), "/packages/respjson") {
			hasField = true
		}
	}
	return hasRaw && hasField
}

// zeroModel describes a zero value for the generator (see valOfJson in
// lean/Go2Lean/Driver.lean).
func zeroModel(v ir.Val) any {
	switch x := v.(type) {
	case ir.IntVal:
		return map[string]any{"k": "int", "w": x.Width}
	case ir.BoolVal:
		return map[string]any{"k": "bool"}
	case ir.ErrVal:
		return map[string]any{"k": "err"}
	case ir.EmptyStr:
		return map[string]any{"k": "str"}
	case ir.OpaqueNil:
		return map[string]any{"k": "nil"}
	case ir.NilPtr:
		return map[string]any{"k": "ptr"}
	case ir.NilSlice:
		return map[string]any{"k": "slice"}
	case ir.StructVal:
		fs := make([]any, len(x.Fields))
		for i, f := range x.Fields {
			fs[i] = zeroModel(f)
		}
		return map[string]any{"k": "struct", "fields": fs}
	case ir.ArrVal:
		es := make([]any, len(x.Elems))
		for i, e := range x.Elems {
			es[i] = zeroModel(e)
		}
		return map[string]any{"k": "arr", "elems": es}
	}
	return map[string]any{"k": "unit"}
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
	case !isForeignUnexported(pkg, t) && isEmptyInterface(t):
		return ab.anyParam(name, src, ipath, extra, stack), nil
	case !isForeignUnexported(pkg, t) && isGeneratedMap(t):
		if mp, ok := ab.mapParam(name, t.Underlying().(*types.Map), src, ipath, extra, stack); ok {
			return mp, nil
		}
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, ExtraPkgs: extra, Model: []string{name}, Kind: contract.ABIOpaque, Zero: zeroLit(t, src)}, nil
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
		if isHandle(pkg, u.Elem()) {
			// A handle another package builds (gorm.Open, NewClient): a
			// non-nil pointer to a value the model does not look into, not
			// one assembled field by field (whose internals a real one
			// never has nil).
			esrc, epath := spell(pkg, u.Elem())
			pointee := contract.ABIParam{Go: name, GoType: esrc, TypePkg: epath, Model: []string{name}, Kind: contract.ABIOpaque, Zero: zeroLit(u.Elem(), esrc)}
			return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIPtr, Pointee: &pointee}, nil
		}
		pointee, err := ab.param(name, u.Elem(), stack)
		if err != nil {
			return contract.ABIParam{}, err
		}
		return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, Model: []string{name}, Kind: contract.ABIPtr, Pointee: &pointee}, nil
	}
	return contract.ABIParam{}, fmt.Errorf("unsupported type %s", t)
}

// isEmptyInterface says whether t is interface{} (any, or a named type
// for it): the inputs the generator fills with values of a few dynamic
// types. Interfaces with methods stay opaque.
func isEmptyInterface(t types.Type) bool {
	it, ok := t.Underlying().(*types.Interface)
	return ok && it.NumMethods() == 0 && !isError(t)
}

// isGeneratedMap says whether t is a map the generator builds: keys of a
// string, integer or bool kind (they compare by value in the model).
func isGeneratedMap(t types.Type) bool {
	m, ok := t.Underlying().(*types.Map)
	if !ok {
		return false
	}
	if isString(m.Key()) || isBool(m.Key()) {
		return true
	}
	_, _, ok = intWidth(m.Key())
	return ok
}

// mapParam describes a map input; false when its key or value cannot be
// described (the map then stays opaque).
func (ab *abiBuilder) mapParam(name string, m *types.Map, src, ipath string, extra []string, stack []*types.Named) (contract.ABIParam, bool) {
	key, err := ab.param("key", m.Key(), stack)
	if err != nil {
		return contract.ABIParam{}, false
	}
	val, err := ab.param("value", m.Elem(), stack)
	if err != nil {
		return contract.ABIParam{}, false
	}
	return contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, ExtraPkgs: extra, Model: []string{name},
		Kind: contract.ABIMap, Key: &key, Value: &val, Zero: "nil"}, true
}

// anyParam describes an empty interface input: nil, or a string, bool or
// int, or (outside another interface's contents) a map[string]interface{}
// or []interface{} of such values, the shapes decoded JSON has.
func (ab *abiBuilder) anyParam(name, src, ipath string, extra []string, stack []*types.Named) contract.ABIParam {
	alts := []types.Type{types.Typ[types.String], types.Typ[types.Bool], types.Typ[types.Int]}
	if ab.anyDepth == 0 {
		empty := types.NewInterfaceType(nil, nil)
		alts = append(alts, types.NewMap(types.Typ[types.String], empty), types.NewSlice(empty))
	}
	ab.anyDepth++
	defer func() { ab.anyDepth-- }()
	p := contract.ABIParam{Go: name, GoType: src, TypePkg: ipath, ExtraPkgs: extra, Model: []string{name}, Kind: contract.ABIAny, Zero: "nil"}
	for _, at := range alts {
		a, err := ab.param("v", at, stack)
		if err != nil {
			continue
		}
		a.Tag = typeTag(at)
		p.Alts = append(p.Alts, a)
	}
	return p
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
