package poc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andreasfoo/go2lean/internal/contract"
)

// result is what the harness prints.
type result struct {
	Panicked bool   `json:"panicked"`
	Panic    string `json:"panic"`
	Ret      string `json:"ret"`
	Pre      *bool  `json:"pre"`
	Spec     *bool  `json:"spec"`
}

// Verify realises and runs one counterexample and writes the verdict and the
// PoC deliverables to the workspace.
func Verify(ws contract.Workspace, root, cxID string) (*contract.Verdict, error) {
	vs, err := VerifyAll(ws, root, []string{cxID})
	if err != nil {
		return nil, err
	}
	return vs[0], nil
}

// prepared is one counterexample with its records and rendered test.
type prepared struct {
	cx     *contract.Counterexample
	prop   *contract.Property
	target *contract.Target
	fn     string
	imps   []string
	v      *contract.Verdict // set early when the harness cannot be built
}

// VerifyAll realises the counterexamples, runs those of each package in a
// single `go test` invocation (compiling the package's tests once), and
// writes a verdict and PoC deliverables for each. If a batch fails to
// compile, its members are run one by one so a single bad harness only
// affects itself.
func VerifyAll(ws contract.Workspace, root string, cxIDs []string) ([]*contract.Verdict, error) {
	byPkg := map[string][]*prepared{}
	var order []string
	all := map[string]*prepared{}
	for _, id := range cxIDs {
		p, err := prepare(ws, id)
		if err != nil {
			return nil, err
		}
		all[id] = p
		if p.v == nil {
			if _, seen := byPkg[p.target.Package]; !seen {
				order = append(order, p.target.Package)
			}
			byPkg[p.target.Package] = append(byPkg[p.target.Package], p)
		}
	}
	for _, pkg := range order {
		batch := byPkg[pkg]
		pkgDir, pkgName, err := goList(root, pkg)
		if err != nil {
			return nil, err
		}
		results, cmd, exit, out, errOut, err := runBatch(root, pkgDir, pkgName, pkg, batch)
		if err != nil {
			return nil, err
		}
		if results == nil && len(batch) > 1 {
			// The batch did not compile: fall back to one test per run.
			for _, p := range batch {
				r, c, e, o, eo, err := runBatch(root, pkgDir, pkgName, pkg, []*prepared{p})
				if err != nil {
					return nil, err
				}
				p.v = verdictOf(p, r, c, e, o, eo)
			}
			continue
		}
		for _, p := range batch {
			p.v = verdictOf(p, results, cmd, exit, out, errOut)
		}
	}
	var vs []*contract.Verdict
	for _, id := range cxIDs {
		p := all[id]
		if p.v == nil {
			return nil, fmt.Errorf("%s: no verdict produced", id)
		}
		if err := deliver(ws, root, p); err != nil {
			return nil, err
		}
		vs = append(vs, p.v)
	}
	return vs, nil
}

func prepare(ws contract.Workspace, cxID string) (*prepared, error) {
	cx, err := ws.Counterexample(cxID)
	if err != nil {
		return nil, err
	}
	prop, err := ws.Property(cx.Property)
	if err != nil {
		return nil, err
	}
	target, err := ws.Target(prop.Target)
	if err != nil {
		return nil, err
	}
	abi, err := ws.ABI(target, cx.Frontend)
	if err != nil {
		return nil, err
	}
	p := &prepared{cx: cx, prop: prop, target: target}
	p.fn, p.imps, err = HarnessFunc(target, prop, cx, abi)
	if err != nil {
		p.v = &contract.Verdict{Counterexample: cx.ID, Status: contract.VerdictInconclusive,
			Diagnosis: &contract.Diagnosis{Reason: "cannot_materialise", Notes: []string{err.Error()}}}
	}
	return p, nil
}

// runBatch writes one test file holding every test of the batch into the
// package, runs it, and returns the parsed results keyed by counterexample
// id (nil when the file did not compile).
func runBatch(root, pkgDir, pkgName, pkg string, batch []*prepared) (map[string]result, string, int, string, string, error) {
	imports := map[string]bool{}
	var funcs []string
	for _, p := range batch {
		funcs = append(funcs, p.fn)
		for _, ip := range p.imps {
			imports[ip] = true
		}
	}
	var imps []string
	for ip := range imports {
		imps = append(imps, ip)
	}
	sort.Strings(imps)
	src := File(pkgName, imps, funcs)
	testPath := filepath.Join(pkgDir, fmt.Sprintf("zz_go2lean_poc_%d_test.go", os.Getpid()))
	if err := os.WriteFile(testPath, []byte(src), 0o644); err != nil {
		return nil, "", 0, "", "", err
	}
	defer os.Remove(testPath)
	run := "^TestGo2LeanPoC_"
	if len(batch) == 1 {
		run += batch[0].cx.ID + "$"
	}
	cmd := exec.Command("go", "test", "-count=1", "-v", "-run", run, pkg)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	exit := 0
	if ee, ok := runErr.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if runErr != nil {
		return nil, "", 0, "", "", runErr
	}
	cmdLine := "go test -count=1 -v -run '" + run + "' " + pkg
	results := parseResults(stdout.String())
	if len(results) == 0 {
		return nil, cmdLine, exit, stdout.String(), stderr.String(), nil
	}
	return results, cmdLine, exit, stdout.String(), stderr.String(), nil
}

func verdictOf(p *prepared, results map[string]result, cmd string, exit int, stdout, stderr string) *contract.Verdict {
	v := &contract.Verdict{
		Counterexample: p.cx.ID,
		Evidence:       contract.Evidence{Cmd: cmd, Exit: exit, StdoutTail: tail(stdout), StderrTail: tail(stderr)},
	}
	res, ok := results[p.cx.ID]
	if !ok {
		v.Status = contract.VerdictInconclusive
		v.Diagnosis = &contract.Diagnosis{Reason: "harness_failed", Notes: []string{"no GO2LEAN_RESULT line; see stdout/stderr"}}
		return v
	}
	judge(v, p.prop, p.cx, res)
	return v
}

// deliver writes the PoC directory (a standalone test file and a README)
// and the verdict.
func deliver(ws contract.Workspace, root string, p *prepared) error {
	v := p.v
	if p.fn != "" {
		pocDir := ws.PocDir(p.cx.ID)
		if err := os.MkdirAll(pocDir, 0o755); err != nil {
			return err
		}
		pkgDir, pkgName, err := goList(root, p.target.Package)
		if err != nil {
			return err
		}
		testName := "zz_go2lean_poc_" + p.cx.ID + "_test.go"
		if err := os.WriteFile(filepath.Join(pocDir, testName), []byte(File(pkgName, p.imps, []string{p.fn})), 0o644); err != nil {
			return err
		}
		cmd := "go test -count=1 -v -run '^TestGo2LeanPoC_" + p.cx.ID + "$' " + p.target.Package
		readme := fmt.Sprintf("# PoC %s\n\nProperty: %s (%s)\n\n%s\n\nVerdict: **%s**\n\nReproduce: copy `%s` into `%s` and run\n\n    %s\n",
			p.cx.ID, p.prop.ID, p.prop.Kind, p.prop.NL, v.Status, testName, pkgDir, cmd)
		if err := os.WriteFile(filepath.Join(pocDir, "README.md"), []byte(readme), 0o644); err != nil {
			return err
		}
		v.PocDir = pocDir
	}
	return ws.WriteVerdict(v)
}

// judge fills Status, Observed and Diagnosis from the harness result.
func judge(v *contract.Verdict, prop *contract.Property, cx *contract.Counterexample, r result) {
	v.Observed = map[string]string{}
	if r.Ret != "" {
		v.Observed["ret"] = r.Ret
	}
	if r.Panicked {
		v.Observed["panic"] = r.Panic
		v.Evidence.PanicMsg = r.Panic
	}
	diag := &contract.Diagnosis{IsCustomProp: prop.Kind == contract.KindCustom}
	for _, h := range cx.Havoc {
		diag.HavocUsed = append(diag.HavocUsed, h.Idx)
	}
	if cx.ModelOutput != nil {
		diag.ModelVsReal = fmt.Sprintf("model: %s %s%s; real: %s", cx.ModelOutput.Res, cx.ModelOutput.Value,
			panicSuffix(string(cx.ModelOutput.Panic)), describe(r))
	}
	if r.Pre != nil && !*r.Pre {
		v.Status = contract.VerdictSpurious
		diag.Reason = "precondition_false"
		v.Diagnosis = diag
		return
	}
	switch prop.Kind {
	case contract.KindNoPanic:
		switch {
		case !r.Panicked:
			v.Status = contract.VerdictSpurious
			diag.Reason = "no_panic_in_real_run"
		case prop.ExpectedPanic == "" || prop.ExpectedPanic == ClassifyPanic(r.Panic):
			v.Status = contract.VerdictConfirmed
			diag = nil
		default:
			v.Status = contract.VerdictInconclusive
			diag.Reason = "panic_kind_mismatch"
			diag.Notes = []string{fmt.Sprintf("expected %s, got %s", prop.ExpectedPanic, ClassifyPanic(r.Panic))}
		}
	case contract.KindPostcondition, contract.KindEquivalence, contract.KindCustom:
		switch {
		case r.Panicked:
			v.Status = contract.VerdictInconclusive
			diag.Reason = "unexpected_panic"
			diag.Notes = []string{"the real run panicked before the oracle could be evaluated; this may be a different bug"}
		case r.Spec == nil:
			v.Status = contract.VerdictInconclusive
			diag.Reason = "no_oracle_result"
		case !*r.Spec:
			v.Status = contract.VerdictConfirmed
			diag = nil
		default:
			v.Status = contract.VerdictSpurious
			diag.Reason = "oracle_holds_in_real_run"
		}
	default:
		v.Status = contract.VerdictInconclusive
		diag.Reason = "unsupported_property_kind"
	}
	v.Diagnosis = diag
}

func panicSuffix(k string) string {
	if k == "" {
		return ""
	}
	return " (" + k + ")"
}

func describe(r result) string {
	if r.Panicked {
		return "panic " + r.Panic
	}
	return "ok " + r.Ret
}

// ClassifyPanic maps a Go runtime panic message to a PanicKind.
func ClassifyPanic(msg string) contract.PanicKind {
	switch {
	case strings.Contains(msg, "index out of range"):
		return contract.PanicIndexOutOfRange
	case strings.Contains(msg, "integer divide by zero"):
		return contract.PanicDivideByZero
	case strings.Contains(msg, "nil pointer dereference"):
		return contract.PanicNilDeref
	case strings.Contains(msg, "negative shift amount"):
		return contract.PanicNegativeShift
	case strings.Contains(msg, "slice bounds out of range"):
		return contract.PanicSliceBounds
	case strings.Contains(msg, "makeslice"):
		return contract.PanicMakeSliceLen
	case strings.HasPrefix(msg, "runtime error:"):
		return contract.PanicOther
	}
	return contract.PanicExplicit
}

// parseResults maps each test's result line to its counterexample id,
// using the "=== RUN TestGo2LeanPoC_<id>" line that precedes it.
func parseResults(stdout string) map[string]result {
	out := map[string]result{}
	current := ""
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(line, "=== RUN   TestGo2LeanPoC_"); ok {
			current = name
			continue
		}
		if rest, ok := strings.CutPrefix(line, "GO2LEAN_RESULT "); ok && current != "" {
			var r result
			if json.Unmarshal([]byte(rest), &r) == nil {
				out[current] = r
			}
		}
	}
	return out
}

func tail(s string) string {
	const n = 2000
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// goList resolves a package's directory and name.
func goList(root, pkg string) (dir, name string, err error) {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}\t{{.Name}}", pkg)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("go list %s: %w", pkg, err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("go list %s: unexpected output %q", pkg, out)
	}
	return parts[0], parts[1], nil
}
