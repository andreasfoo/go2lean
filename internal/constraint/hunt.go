package constraint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
}

func (o SearchOptions) withDefaults() SearchOptions {
	if o.Trials == 0 {
		o.Trials = 2000
	}
	if o.Fuel == 0 {
		o.Fuel = 1000
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
	Inputs      map[string]string          `json:"inputs"`
	Memory      map[string][]string        `json:"memory"`
	Objects     map[string]json.RawMessage `json:"objects"`
	Havoc       []contract.HavocValue      `json:"havoc"`
	ModelOutput *contract.ModelOutput      `json:"model_output"`
}

// gens renders the Search.Gen list for a function's parameters.
func gens(abi *contract.ABI, maxLen int) (string, error) {
	var parts []string
	for _, p := range abi.Params {
		g, err := gen(abi, p, maxLen)
		if err != nil {
			return "", err
		}
		parts = append(parts, g)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
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
func typeTable(abi *contract.ABI, maxLen int) (string, error) {
	var entries []string
	for _, t := range abi.Types {
		var fs []string
		for _, f := range t.Fields {
			g, err := gen(abi, f, maxLen)
			if err != nil {
				return "", err
			}
			fs = append(fs, g)
		}
		entries = append(entries, "["+strings.Join(fs, ", ")+"]")
	}
	return "#[" + strings.Join(entries, ", ") + "]", nil
}

func gen(abi *contract.ABI, p contract.ABIParam, maxLen int) (string, error) {
	switch p.Kind {
	case contract.ABIInt:
		w, signed, ok := p.IntInfo()
		if !ok {
			return "", fmt.Errorf("parameter %s: unsupported int type %s", p.Go, p.GoType)
		}
		return fmt.Sprintf("(.int %q %d %v)", p.Go, w, signed), nil
	case contract.ABIBool:
		return fmt.Sprintf("(.bool %q)", p.Go), nil
	case contract.ABIError:
		return fmt.Sprintf("(.err %q)", p.Go), nil
	case contract.ABISlice:
		if p.ElemParam != nil {
			e, err := gen(abi, *p.ElemParam, maxLen)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(.sliceOf %q %s %d)", p.Go, e, maxLen), nil
		}
		w, signed, ok := p.ElemInfo()
		if !ok {
			return "", fmt.Errorf("parameter %s: unsupported element type %s", p.Go, p.Elem)
		}
		return fmt.Sprintf("(.slice %q %d %v %d)", p.Go, w, signed, maxLen), nil
	case contract.ABIStruct:
		if p.TypeRef != "" {
			return fmt.Sprintf("(.ref %q %d)", p.Go, typeIndex(abi, p.TypeRef)), nil
		}
		var fs []string
		for _, f := range p.Fields {
			g, err := gen(abi, f, maxLen)
			if err != nil {
				return "", err
			}
			fs = append(fs, g)
		}
		return fmt.Sprintf("(.structv %q [%s])", p.Go, strings.Join(fs, ", ")), nil
	case contract.ABIString:
		return fmt.Sprintf("(.str %q %d)", p.Go, maxLen), nil
	case contract.ABIOpaque:
		return fmt.Sprintf("(.opaque %q)", p.Go), nil
	case contract.ABIPtr:
		if p.Pointee == nil {
			// The frontend's depth cut-off for recursive types: always nil.
			return fmt.Sprintf("(.nilptr %q)", p.Go), nil
		}
		g, err := gen(abi, *p.Pointee, maxLen)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(.ptr %q %s)", p.Go, g), nil
	}
	return "", fmt.Errorf("parameter %s: unsupported kind %s", p.Go, p.Kind)
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
	var consts, strConsts []string
	for _, f := range report.Funcs {
		if f.Name == target.Func {
			fid = f.ID
			consts = append(consts, f.Consts...)
			strConsts = append(strConsts, f.StrConsts...)
		}
		if prop.Spec != nil && f.Name == prop.Spec.GoFunc {
			sid = f.ID
			consts = append(consts, f.Consts...)
			strConsts = append(strConsts, f.StrConsts...)
		}
	}
	if fid < 0 {
		return nil, "", fmt.Errorf("function %s not in model report", target.Func)
	}
	var leanProp string
	switch prop.Kind {
	case contract.KindNoPanic:
		leanProp = fmt.Sprintf("(.noPanic %d)", fid)
	case contract.KindPostcondition:
		if sid < 0 {
			return nil, "", fmt.Errorf("spec %s not in model report", prop.Spec.GoFunc)
		}
		leanProp = fmt.Sprintf("(.postcondition %d %d)", fid, sid)
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

	extra := "[" + strings.Join(consts, ", ") + "]"
	quoted := make([]string, len(strConsts))
	for i, sc := range strConsts {
		quoted[i] = leanString(sc)
	}
	strs := "[" + strings.Join(quoted, ", ") + "]"
	leanDir := repo.LeanDir(root)
	if out, err := lake(leanDir, "build", "Go2Lean.Search", report.LeanModule); err != nil {
		return nil, "", fmt.Errorf("lake build %s: %w\n%s", report.LeanModule, err, out)
	}
	prog := report.LeanModule + ".prog"

	var summary []string
	if opts.Engine == EngineRandom || opts.Engine == EngineAll {
		call := fmt.Sprintf("Search.run %s %s %s %s %d %d %d %s %s %v", prog, leanProp, g, extra, opts.Seed, opts.Trials, opts.Fuel, strs, table, opts.NilInputs)
		res, err := runSearch(leanDir, report.LeanModule, prop.ID+"_random", call)
		if err != nil {
			return nil, "", err
		}
		if res.Found {
			cx, err := writeCx(ws, prop, res, "random", opts)
			return cx, fmt.Sprintf("random: found at trial %d", res.Trial), err
		}
		summary = append(summary, fmt.Sprintf("random: none in %d trials (%d out of fuel, %d moot/stuck)", res.Trials, res.OutOfFuel, res.Moot))
	}
	if opts.Engine == EngineExhaustive || opts.Engine == EngineAll {
		call := fmt.Sprintf("Search.exhaust %s %s %s %s %d %d %d %s %s %v", prog, leanProp, g, extra, opts.ExhaustLen, opts.ExhaustLimit, opts.Fuel, strs, table, opts.NilInputs)
		res, err := runSearch(leanDir, report.LeanModule, prop.ID+"_exhaustive", call)
		if err != nil {
			return nil, "", err
		}
		switch {
		case res.Found:
			cx, err := writeCx(ws, prop, res, "exhaustive", opts)
			return cx, fmt.Sprintf("exhaustive: found at input %d", res.Trial), err
		case res.Exhaustive:
			domain := fmt.Sprintf("%.0f inputs: boundary values, mined literals and |v|<=8, slices up to length %d", res.Domain, res.MaxLen)
			if res.HavocSites > 0 {
				domain += fmt.Sprintf("; %d havoc sites not enumerated", res.HavocSites)
			}
			pr := &contract.ProofResult{Property: prop.ID, Status: contract.ProofExhausted, Fuel: opts.Fuel, Tactic: "exhaustive", Domain: domain}
			if err := ws.WriteProofResult(pr); err != nil {
				return nil, "", err
			}
			summary = append(summary, fmt.Sprintf("exhaustive: holds on %.0f inputs (recorded as %s)", res.Domain, pr.Status))
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

// runSearch writes a one-line Lean file that evaluates call and parses the
// JSON line it prints.
func runSearch(leanDir, module, name, call string) (*searchResult, error) {
	searchFile := filepath.Join(leanDir, "Search", name+".lean")
	src := fmt.Sprintf(`import Go2Lean.Search
import %s
open Go2Lean Go2Lean.Search
-- Generated by go2lean. Do not edit.
#eval %s
`, module, call)
	if err := os.MkdirAll(filepath.Dir(searchFile), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(searchFile, []byte(src), 0o644); err != nil {
		return nil, err
	}
	out, err := lake(leanDir, "env", "lean", searchFile)
	if err != nil {
		return nil, fmt.Errorf("lean %s: %w\n%s", searchFile, err, out)
	}
	line := lastLine(out)
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

// leanString renders a Go string as a Lean string literal.
func leanString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
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
