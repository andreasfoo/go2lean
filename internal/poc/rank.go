package poc

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"

	"github.com/andreasfoo/go2lean/internal/contract"
)

// Ranking orders confirmed findings by how plausible their input is for the
// code's real callers. A confirmed verdict shows that the counterexample
// reproduces, not that a caller can build it: a nil argument no caller
// passes, or a value the test forged, is a finding about the test.
//
//	1: inputs any caller of the package's API can pass, or a call site in
//	   the module passes the same nil
//	2: plain inputs to an unexported function (depends on its callers), or
//	   a hand-written spec violated
//	3: a nil argument no call site in the module passes, or an
//	   agent-written spec violated (check the spec)
//	4: an input the test forged (an opaque value left nil, package state
//	   the program may initialise first)

// rankOf ranks one confirmed verdict.
func rankOf(p *prepared, callers *callerIndex) *contract.Rank {
	if p.prop.Kind != contract.KindNoPanic {
		if p.prop.Source == "agent" {
			return &contract.Rank{Tier: 3, Reason: "agent-written spec violated: check that the spec is right"}
		}
		return &contract.Rank{Tier: 2, Reason: "hand-written spec violated"}
	}
	if d := p.v.Diagnosis; d != nil {
		switch d.Reason {
		case "nil_opaque_input":
			return &contract.Rank{Tier: 4, Reason: "the test left an opaque input nil (interface, map, func or another package's private field)"}
		case "reads_unknown_state":
			return &contract.Rank{Tier: 4, Reason: "reads package state the program may initialise before such calls"}
		}
	}
	fn := baseFunc(p.target.Func)
	if nils := nilArguments(p.cx); len(nils) > 0 {
		what := "nil for " + strings.Join(nils, ", ")
		sites, calls, ok := callers.nilSites(p.target.Package, fn, nils)
		switch {
		case len(sites) > 0:
			return &contract.Rank{Tier: 1, Reason: "a call site in the module passes " + what, Sites: sites}
		case !ok:
			return &contract.Rank{Tier: 3, Reason: what + ": the module's call sites could not be loaded"}
		case calls == 0:
			return &contract.Rank{Tier: 3, Reason: what + ": no static call site in the module (called through an interface, or from outside)"}
		default:
			return &contract.Rank{Tier: 3, Reason: fmt.Sprintf("%s: none of its %d call sites in the module passes nil there", what, calls)}
		}
	}
	if exportedFunc(fn) {
		return &contract.Rank{Tier: 1, Reason: "plain inputs to the package's API"}
	}
	return &contract.Rank{Tier: 2, Reason: "plain inputs to an unexported function: check its callers"}
}

// baseFunc strips what the target id adds to a function's name: the
// constructor of its receiver (`T.M~NewT`) and type arguments.
func baseFunc(f string) string {
	f, _, _ = strings.Cut(f, "~")
	f, _, _ = strings.Cut(f, "[")
	return f
}

// exportedFunc says whether a function (`F`) or method (`T.M`) is part of
// its package's API; closures (`F$1`) are not.
func exportedFunc(f string) bool {
	if strings.Contains(f, "$") {
		return false
	}
	for _, part := range strings.Split(f, ".") {
		part = strings.TrimLeft(part, "(*")
		if part == "" || !unicode.IsUpper([]rune(part)[0]) {
			return false
		}
	}
	return true
}

// callerIndex holds the module's call sites, loaded on first use.
type callerIndex struct {
	root   string
	loaded bool
	failed bool
	// calls maps "pkg.F" or "pkg.T.M" to its call sites.
	calls map[string][]callSite
	// params maps the same key to the callee's parameter names.
	params map[string][]string
}

type callSite struct {
	pos token.Position
	nil []bool // per argument: the literal nil
}

func newCallerIndex(root string) *callerIndex { return &callerIndex{root: root} }

func (c *callerIndex) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	c.calls, c.params = map[string][]callSite{}, map[string][]string{}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:  c.root,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		c.failed = true
		return
	}
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				f, ok := typeutil.Callee(pkg.TypesInfo, call).(*types.Func)
				if !ok || f.Pkg() == nil {
					return true
				}
				key := funcKey(f)
				site := callSite{pos: pkg.Fset.Position(call.Pos())}
				for _, a := range call.Args {
					tv := pkg.TypesInfo.Types[a]
					site.nil = append(site.nil, tv.IsNil())
				}
				c.calls[key] = append(c.calls[key], site)
				if _, ok := c.params[key]; !ok {
					sig := f.Type().(*types.Signature)
					var names []string
					for i := 0; i < sig.Params().Len(); i++ {
						names = append(names, sig.Params().At(i).Name())
					}
					c.params[key] = names
				}
				return true
			})
		}
	}
}

// funcKey is "pkg.F" or "pkg.T.M" (pointer receivers as T).
func funcKey(f *types.Func) string {
	f = f.Origin()
	sig := f.Type().(*types.Signature)
	if recv := sig.Recv(); recv != nil {
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := t.(*types.Named); ok {
			return f.Pkg().Path() + "." + n.Obj().Name() + "." + f.Name()
		}
	}
	return f.Pkg().Path() + "." + f.Name()
}

// nilSites lists the call sites of pkg.fn that pass the literal nil for
// one of the named parameters, and counts its call sites; ok is false when
// the module's code could not be loaded.
func (c *callerIndex) nilSites(pkg, fn string, names []string) (sites []string, calls int, ok bool) {
	c.load()
	if c.failed {
		return nil, 0, false
	}
	key := pkg + "." + fn
	params := c.params[key]
	for _, s := range c.calls[key] {
		for i, isNil := range s.nil {
			if isNil && i < len(params) && containsName(names, params[i]) {
				sites = append(sites, fmt.Sprintf("%s:%d", s.pos.Filename, s.pos.Line))
				break
			}
		}
	}
	sort.Strings(sites)
	return sites, len(c.calls[key]), true
}

func containsName(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

// RankAll ranks the confirmed verdicts already in the workspace (written
// before ranking existed, or against older code) and writes them back.
func RankAll(ws contract.Workspace, root string) ([]*contract.Verdict, error) {
	files, err := filepath.Glob(filepath.Join(ws.Root, "verdicts", "*.json"))
	if err != nil {
		return nil, err
	}
	callers := newCallerIndex(root)
	var out []*contract.Verdict
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		v := ws.Verdict(id)
		if v == nil || v.Status != contract.VerdictConfirmed {
			continue
		}
		p, err := prepare(ws, id)
		if err != nil {
			return nil, err
		}
		p.v = v
		v.Rank = rankOf(p, callers)
		if err := ws.WriteVerdict(v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank.Tier < out[j].Rank.Tier })
	return out, nil
}
