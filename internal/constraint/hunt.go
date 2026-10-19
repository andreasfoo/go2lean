package constraint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/repo"
)

// Engine selects which counterexample engines Hunt runs.
type Engine string

const (
	EngineRandom     Engine = "random"
	EngineExhaustive Engine = "exhaustive"
	// EngineAll runs random search first and, when it finds nothing, the
	// exhaustive engine so that a small domain can be certified.
	EngineAll Engine = "all"
)

// SearchOptions tune the engines.
type SearchOptions struct {
	Engine Engine
	Seed   int
	Trials int
	Fuel   int
	MaxLen int
	// ExhaustLen bounds slice lengths in the exhaustive engine; ExhaustLimit
	// bounds the domain size it will attempt.
	ExhaustLen   int
	ExhaustLimit int
	// NilInputs lets top-level pointer arguments and receivers be nil. Off by
	// default: "it panics on a nil receiver" is true of most Go code and
	// drowns the findings people act on. Nested pointers may still be nil.
	NilInputs bool
	// Jobs is how many properties HuntAll searches at once (default: the
	// number of CPUs); each search is its own process.
	Jobs int
	// BudgetSeconds bounds the wall-clock time of each engine on one property
	// (default 10; 0 for none). A property with expensive inputs then
	// reports fewer trials instead of stalling the sweep.
	BudgetSeconds int
}

func (o SearchOptions) withDefaults() SearchOptions {
	if o.Trials == 0 {
		o.Trials = 2000
	}
	if o.Fuel == 0 {
		o.Fuel = 1000
	}
	if o.Jobs <= 0 {
		o.Jobs = runtime.NumCPU()
	}
	if o.BudgetSeconds == 0 {
		o.BudgetSeconds = 10
	}
	if o.MaxLen == 0 {
		o.MaxLen = 8
	}
	if o.Engine == "" {
		o.Engine = EngineAll
	}
	if o.ExhaustLen == 0 {
		o.ExhaustLen = 3
	}
	if o.ExhaustLimit == 0 {
		o.ExhaustLimit = 300000
	}
	return o
}

// searchResult is the JSON line Go2Lean.Search.run prints.
type searchResult struct {
	Found       bool                       `json:"found"`
	Trial       int                        `json:"trial"`
	Seed        int                        `json:"seed"`
	Trials      int                        `json:"trials"`
	OutOfFuel   int                        `json:"out_of_fuel"`
	Moot        int                        `json:"moot"` // postcondition runs where the target panicked first
	Exhaustive  bool                       `json:"exhaustive"`
	Domain      float64                    `json:"domain"` // may exceed int64 when the domain is too large
	Reason      string                     `json:"reason"`
	MaxLen      int                        `json:"max_len"`
	HavocSites  int                        `json:"havoc_sites"`
	BudgetHit   bool                       `json:"budget_hit"`
	StuckSites  map[string]int             `json:"stuck_sites"` // where the model got stuck, by count
	SpecExits   map[string]int             `json:"spec_exits"`  // spec return block -> trials that left through it
	Checked     int                        `json:"checked"`
	Inputs      map[string]string          `json:"inputs"`
	Memory      map[string][]string        `json:"memory"`
	Objects     map[string]json.RawMessage `json:"objects"`
	Havoc       []contract.HavocValue      `json:"havoc"`
	ModelOutput *contract.ModelOutput      `json:"model_output"`
}

// gens renders the Search.Gen list for a function's parameters.
func gens(abi *contract.ABI, maxLen int) ([]map[string]any, error) {
	out := []map[string]any{}
	for _, p := range abi.Params {
		g, err := gen(abi, p, maxLen)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func typeIndex(abi *contract.ABI, ref string) int {
	for i, t := range abi.Types {
		if t.GoType == ref {
			return i
		}
	}
	return -1
}

// typeTable renders the Search.TypeTable for the ABI's recursive types.
func typeTable(abi *contract.ABI, maxLen int) ([][]map[string]any, error) {
	entries := [][]map[string]any{}
	for _, t := range abi.Types {
		fs := []map[string]any{}
		for _, f := range t.Fields {
			g, err := genIn(abi, f, maxLen, []string{t.GoType})
			if err != nil {
				return nil, err
			}
			fs = append(fs, g)
		}
		entries = append(entries, fs)
	}
	return entries, nil
}

// gen describes the Search.Gen for one parameter as JSON; the shape is
// decoded by `genOfJson` in lean/Go2Lean/Driver.lean.
func gen(abi *contract.ABI, p contract.ABIParam, maxLen int) (map[string]any, error) {
	g, err := genIn(abi, p, maxLen, nil)
	if err == nil && p.Kind == contract.ABIOpaque && p.GoType == "context.Context" {
		g["v"] = "nonnil" // M3 passes context.Background() for a context parameter
	}
	return g, err
}

// genIn is gen along a path of the struct types under construction: a
// pointer to one of them is a back-edge of a recursive type (`link`),
// nullable at every depth, where any other pointer is non-nil by default.
func genIn(abi *contract.ABI, p contract.ABIParam, maxLen int, open []string) (map[string]any, error) {
	switch p.Kind {
	case contract.ABIInt:
		w, signed, ok := p.IntInfo()
		if !ok {
			return nil, fmt.Errorf("parameter %s: unsupported int type %s", p.Go, p.GoType)
		}
		return map[string]any{"k": "int", "name": p.Go, "w": w, "signed": signed}, nil
	case contract.ABIBool:
		return map[string]any{"k": "bool", "name": p.Go}, nil
	case contract.ABIError:
		return map[string]any{"k": "err", "name": p.Go}, nil
	case contract.ABISlice:
		if p.ElemParam != nil {
			e, err := genIn(abi, *p.ElemParam, maxLen, open)
			if err != nil {
				return nil, err
			}
			return map[string]any{"k": "sliceOf", "name": p.Go, "elem": e, "maxLen": maxLen}, nil
		}
		w, signed, ok := p.ElemInfo()
		if !ok {
			return nil, fmt.Errorf("parameter %s: unsupported element type %s", p.Go, p.Elem)
		}
		return map[string]any{"k": "slice", "name": p.Go, "w": w, "signed": signed, "maxLen": maxLen}, nil
	case contract.ABIStruct:
		if p.TypeRef != "" {
			return map[string]any{"k": "ref", "name": p.Go, "ty": typeIndex(abi, p.TypeRef)}, nil
		}
		fs := []map[string]any{}
		for _, f := range p.Fields {
			g, err := genIn(abi, f, maxLen, append(open, p.GoType))
			if err != nil {
				return nil, err
			}
			fs = append(fs, g)
		}
		return map[string]any{"k": "structv", "name": p.Go, "fields": fs}, nil
	case contract.ABIString:
		if p.MaxLen < 0 {
			return map[string]any{"k": "str", "name": p.Go, "maxLen": 0}, nil
		}
		g := map[string]any{"k": "str", "name": p.Go, "maxLen": maxLen}
		if len(p.Enum) > 0 {
			g["enum"] = p.Enum
		}
		return g, nil
	case contract.ABIOpaque:
		// M3 passes the zero value: nil for a func, map, channel or
		// interface (its ABI zero literal is "nil"), so the model may treat
		// the input as nil; other opaque values (floats, opaque structs)
		// stay unknown.
		v := "unknown"
		if p.Zero == "nil" {
			v = "nil"
		}
		return map[string]any{"k": "opaque", "name": p.Go, "v": v}, nil
	case contract.ABIArray:
		e, err := genIn(abi, p.ElemAsParam(), maxLen, open)
		if err != nil {
			return nil, err
		}
		return map[string]any{"k": "arrOf", "name": p.Go, "elem": e, "n": p.Len}, nil
	case contract.ABIPtr:
		if p.Pointee == nil {
			// The frontend's depth cut-off for recursive types: always nil.
			return map[string]any{"k": "nilptr", "name": p.Go}, nil
		}
		g, err := genIn(abi, *p.Pointee, maxLen, open)
		if err != nil {
			return nil, err
		}
		kind := "ptr"
		if p.Pointee.TypeRef != "" && slices.Contains(open, p.Pointee.TypeRef) {
			kind = "link"
		}
		return map[string]any{"k": kind, "name": p.Go, "pointee": g}, nil
	}
	return nil, fmt.Errorf("parameter %s: unsupported kind %s", p.Go, p.Kind)
}

// literalPool collects the integer and string literals of the given
// functions and, transitively, of the functions they call.
func literalPool(ws contract.Workspace, report *contract.ModelReport, roots []int) (consts, strConsts []string) {
	reports := map[string]*contract.ModelReport{report.Package: report}
	byID := map[contract.XCall]contract.ModelFunc{}
	index := func(r *contract.ModelReport) {
		for _, f := range r.Funcs {
			byID[contract.XCall{Package: r.Package, ID: f.ID}] = f
		}
	}
	index(report)
	seen := map[contract.XCall]bool{}
	var walk func(x contract.XCall)
	walk = func(x contract.XCall) {
		if _, loaded := reports[x.Package]; !loaded {
			// A sibling package's report, when it was compiled alongside.
			r, err := ws.ModelReport(report.Frontend, x.Package)
			reports[x.Package] = r
			if err == nil {
				index(r)
			}
		}
		f, ok := byID[x]
		if !ok || seen[x] {
			return
		}
		seen[x] = true
		for _, c := range f.Consts {
			if !slices.Contains(consts, c) {
				consts = append(consts, c)
			}
		}
		for _, s := range f.StrConsts {
			if !slices.Contains(strConsts, s) {
				strConsts = append(strConsts, s)
			}
		}
		for _, callee := range f.Calls {
			walk(contract.XCall{Package: x.Package, ID: callee})
		}
		for _, callee := range f.XCalls {
			walk(callee)
		}
	}
	for _, r := range roots {
		if r >= 0 {
			walk(contract.XCall{Package: report.Package, ID: r})
		}
	}
	return consts, strConsts
}

// Hunt runs the selected engines on one property. It returns the
// counterexample it wrote to the workspace (nil when none was found) and a
// one-line summary. When the exhaustive engine covers its whole domain
// without a counterexample, a ProofResult with status "exhausted" is written.
func Hunt(ws contract.Workspace, root string, prop *contract.Property, opts SearchOptions) (*contract.Counterexample, string, error) {
	opts = opts.withDefaults()
	target, err := ws.Target(prop.Target)
	if err != nil {
		return nil, "", err
	}
	abi, err := ws.ABI(target, prop.Frontend)
	if err != nil {
		return nil, "", err
	}
	report, err := ws.ModelReport(prop.Frontend, target.Package)
	if err != nil {
		return nil, "", err
	}
	fid, sid := -1, -1
	for _, f := range report.Funcs {
		if f.Name == target.Func {
			fid = f.ID
		}
		if prop.Spec != nil && f.Name == prop.Spec.GoFunc {
			sid = f.ID
		}
	}
	// The literal pool: the target's and the spec's constants and those of
	// everything they call, so the branch a callee takes on "assistant" is
	// reachable from the caller's inputs.
	consts, strConsts := literalPool(ws, report, []int{fid, sid})
	var specExits []contract.ExitSite
	for _, f := range report.Funcs {
		if f.ID == sid && sid >= 0 {
			specExits = f.Exits
		}
	}
	if fid < 0 {
		return nil, "", fmt.Errorf("function %s not in model report", target.Func)
	}
	var leanProp map[string]any
	switch prop.Kind {
	case contract.KindNoPanic:
		leanProp = map[string]any{"kind": "noPanic", "f": fid}
	case contract.KindPostcondition:
		if sid < 0 {
			return nil, "", fmt.Errorf("spec %s not in model report", prop.Spec.GoFunc)
		}
		leanProp = map[string]any{"kind": "postcondition", "f": fid, "spec": sid}
	default:
		return nil, "", fmt.Errorf("property kind %s has no search engine yet", prop.Kind)
	}
	g, err := gens(abi, opts.MaxLen)
	if err != nil {
		return nil, "", err
	}
	table, err := typeTable(abi, opts.MaxLen)
	if err != nil {
		return nil, "", err
	}

	extra := []json.Number{}
	for _, c := range consts {
		extra = append(extra, json.Number(c))
	}
	if strConsts == nil {
		strConsts = []string{}
	}
	leanDir := repo.LeanDir(root)
	if err := buildDriver(leanDir); err != nil {
		return nil, "", err
	}
	spec := map[string]any{
		"prog": strings.TrimPrefix(report.LeanModule, "Go2Lean.Generated."), "prop": leanProp,
		"gens": g, "extra": extra, "strs": strConsts, "table": table, "allowNil": opts.NilInputs,
		"seed": opts.Seed, "trials": opts.Trials, "fuel": opts.Fuel,
		"maxLen": opts.ExhaustLen, "limit": opts.ExhaustLimit,
		"budgetMs": max(opts.BudgetSeconds, 0) * 1000,
	}

	var summary []string
	if opts.Engine == EngineRandom || opts.Engine == EngineAll {
		res, err := runSearch(leanDir, prop.ID+"_random", spec, "random")
		if err != nil {
			return nil, "", err
		}
		if res.Found {
			cx, err := writeCx(ws, prop, res, "random", opts)
			return cx, fmt.Sprintf("random: found at trial %d", res.Trial), err
		}
		note := ""
		if res.BudgetHit {
			note = fmt.Sprintf(" [stopped by the %ds budget]", opts.BudgetSeconds)
		}
		summary = append(summary, fmt.Sprintf("random: none in %d trials (%d out of fuel, %d moot/stuck)%s%s%s", res.Trials, res.OutOfFuel, res.Moot, exitNote(specExits, res.SpecExits), note, stuckNote(res.StuckSites, report.Havoc, reportsByKey(ws, report.Frontend, res.StuckSites))))
	}
	if opts.Engine == EngineExhaustive || opts.Engine == EngineAll {
		res, err := runSearch(leanDir, prop.ID+"_exhaustive", spec, "exhaustive")
		if err != nil {
			return nil, "", err
		}
		switch {
		case res.Found:
			cx, err := writeCx(ws, prop, res, "exhaustive", opts)
			return cx, fmt.Sprintf("exhaustive: found at input %d", res.Trial), err
		case res.Exhaustive:
			domain := fmt.Sprintf("%.0f inputs: boundary values, mined literals and |v|<=8, slices up to length %d, strings of a named type limited to its constants, \"\" and \"a\"", res.Domain, res.MaxLen)
			if prop.Kind == contract.KindPostcondition {
				_, checked := exitCounts(specExits, res.SpecExits)
				domain += fmt.Sprintf("; %d of them got past the spec's guards", checked)
			}
			if res.HavocSites > 0 {
				domain += fmt.Sprintf("; %d havoc sites not enumerated", res.HavocSites)
			}
			pr := &contract.ProofResult{Property: prop.ID, Status: contract.ProofExhausted, Fuel: opts.Fuel, Tactic: "exhaustive", Domain: domain}
			if err := ws.WriteProofResult(pr); err != nil {
				return nil, "", err
			}
			summary = append(summary, fmt.Sprintf("exhaustive: holds on %.0f inputs%s (recorded as %s)", res.Domain, exitNote(specExits, res.SpecExits), pr.Status))
		case res.Reason == "budget":
			summary = append(summary, fmt.Sprintf("exhaustive: stopped by the %ds budget after %d of %.0f inputs", opts.BudgetSeconds, res.Checked, res.Domain))
		case res.Reason == "signature too wide":
			summary = append(summary, fmt.Sprintf("exhaustive: skipped (%s: %.0f leaf generators > 24)", res.Reason, res.Domain))
		case res.Reason != "":
			summary = append(summary, fmt.Sprintf("exhaustive: skipped (%s: %.3g > %d)", res.Reason, res.Domain, opts.ExhaustLimit))
		case res.Moot > 0:
			summary = append(summary, fmt.Sprintf("exhaustive: no certificate, %d of %.0f inputs were moot (the model got stuck, or the target panicked before the oracle ran)", res.Moot, res.Domain))
		default:
			summary = append(summary, fmt.Sprintf("exhaustive: no certificate, %d inputs ran out of fuel", res.OutOfFuel))
		}
	}
	return nil, strings.Join(summary, "; "), nil
}

// stuckNote names the most frequent site at which the model got stuck.
func stuckNote(sites map[string]int, havoc []contract.ABIHavoc, reports map[string]*contract.ModelReport) string {
	best, n := "", 0
	for s, c := range sites {
		if c > n || c == n && s < best {
			best, n = s, c
		}
	}
	if n == 0 {
		return ""
	}
	// An assumption is named by its havoc site; sites past the target's
	// own program belong to a linked package.
	if rest, ok := strings.CutPrefix(best, "assumption at havoc site "); ok {
		// "<model key>#<local index>" in a linked program, else a plain index.
		pkgKey, idx, linked := strings.Cut(rest, "#")
		if !linked {
			idx = pkgKey
		}
		if i, err := strconv.Atoi(idx); err == nil {
			switch {
			case linked && reports[pkgKey] != nil && i < len(reports[pkgKey].Havoc):
				best = "an assumed value: " + reports[pkgKey].Havoc[i].Site
			case !linked && i < len(havoc):
				best = "an assumed value: " + havoc[i].Site
			default:
				best = "an assumed value at " + rest
			}
		}
	}
	return fmt.Sprintf("; stuck mostly at %s (%d)", best, n)
}

// driverBuilt remembers that the search executable is up to date with the
// generated models; InvalidateDriver resets it after a compile.
var (
	driverMu    sync.Mutex
	driverBuilt string
)

// InvalidateDriver makes the next Hunt rebuild the search executable.
func InvalidateDriver() {
	driverMu.Lock()
	defer driverMu.Unlock()
	driverBuilt = ""
}

// buildDriver links the native search driver (lean/Main.lean) against
// every generated model once per compile.
func buildDriver(leanDir string) error {
	driverMu.Lock()
	defer driverMu.Unlock()
	if driverBuilt == leanDir {
		return nil
	}
	if out, err := lake(leanDir, "build", "go2lean-search"); err != nil {
		return fmt.Errorf("lake build go2lean-search: %w\n%s", err, out)
	}
	driverBuilt = leanDir
	return nil
}

// runSearch writes the search description under lean/Search and runs the
// native driver on it, parsing the JSON line it prints.
func runSearch(leanDir, name string, spec map[string]any, engine string) (*searchResult, error) {
	specFile := filepath.Join(leanDir, "Search", name+".json")
	spec["engine"] = engine
	src, err := json.MarshalIndent(spec, "", " ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(specFile), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(specFile, src, 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command(filepath.Join(leanDir, ".lake", "build", "bin", "go2lean-search"), specFile)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go2lean-search %s: %w\n%s", specFile, err, buf.String())
	}
	line := lastLine(buf.String())
	var res searchResult
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return nil, fmt.Errorf("parsing search output %q: %w", line, err)
	}
	return &res, nil
}

func writeCx(ws contract.Workspace, prop *contract.Property, res *searchResult, engine string, opts SearchOptions) (*contract.Counterexample, error) {
	cx := &contract.Counterexample{
		ID:          "cx_" + strings.TrimPrefix(prop.ID, "p_") + "_" + engine + fmt.Sprintf("_s%d", opts.Seed),
		Property:    prop.ID,
		Frontend:    prop.Frontend,
		Engine:      engine,
		Fuel:        opts.Fuel,
		Inputs:      res.Inputs,
		Memory:      res.Memory,
		Objects:     res.Objects,
		Havoc:       res.Havoc,
		ModelOutput: res.ModelOutput,
		// The reported values are the ones the model actually executed on.
		RevalidatedInLean: true,
	}
	return cx, ws.WriteCounterexample(cx)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// lake runs the lake binary, falling back to elan's default location.
func lake(dir string, args ...string) (string, error) {
	bin, err := exec.LookPath("lake")
	if err != nil {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".elan", "bin", "lake")
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	return buf.String(), err
}

// exitCounts splits the trials that ran a spec to its end into those that
// left through a `return true` guard (vacuous) and the others (checked).
func exitCounts(sites []contract.ExitSite, exits map[string]int) (vacuous, checked int) {
	for b, n := range exits {
		vac := false
		for _, s := range sites {
			if strconv.Itoa(s.Block) == b && s.ConstTrue && !s.Last {
				vac = true
			}
		}
		if vac {
			vacuous += n
		} else {
			checked += n
		}
	}
	return vacuous, checked
}

// exitNote says how many trials got past the spec's guards, and through
// which return statements (by source line) they left.
func exitNote(sites []contract.ExitSite, exits map[string]int) string {
	if len(exits) == 0 {
		return ""
	}
	vacuous, checked := exitCounts(sites, exits)
	var parts []string
	for _, s := range sites {
		n := exits[strconv.Itoa(s.Block)]
		if n == 0 {
			continue
		}
		label := fmt.Sprintf("line %d", s.Line)
		if s.ConstTrue && !s.Last {
			label += " (return true)"
		}
		parts = append(parts, fmt.Sprintf("%s: %d", label, n))
	}
	return fmt.Sprintf("; spec checked %d, vacuous %d [%s]", checked, vacuous, strings.Join(parts, ", "))
}

// reportsByKey loads the model reports of the linked packages a stuck
// site names ("<module key>#<index>"), keyed by module key.
func reportsByKey(ws contract.Workspace, frontend string, sites map[string]int) map[string]*contract.ModelReport {
	out := map[string]*contract.ModelReport{}
	for site := range sites {
		rest, ok := strings.CutPrefix(site, "assumption at havoc site ")
		if !ok {
			continue
		}
		key, _, linked := strings.Cut(rest, "#")
		if !linked || out[key] != nil {
			continue
		}
		if r, err := ws.ModelReportByKey(frontend, strings.TrimPrefix(key, frontend+".")); err == nil {
			out[key] = r
		}
	}
	return out
}
