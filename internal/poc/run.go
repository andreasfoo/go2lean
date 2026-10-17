package poc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	pkgDir, pkgName, err := goList(root, target.Package)
	if err != nil {
		return nil, err
	}
	src, err := Harness(pkgName, target, prop, cx, abi)
	if err != nil {
		return nil, err
	}

	pocDir := ws.PocDir(cx.ID)
	if err := os.MkdirAll(pocDir, 0o755); err != nil {
		return nil, err
	}
	testName := "zz_go2lean_poc_" + cx.ID + "_test.go"
	if err := os.WriteFile(filepath.Join(pocDir, testName), []byte(src), 0o644); err != nil {
		return nil, err
	}
	// Drop the test into the package, run it, remove it.
	testPath := filepath.Join(pkgDir, testName)
	if err := os.WriteFile(testPath, []byte(src), 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(testPath)

	run := "^TestGo2LeanPoC_" + cx.ID + "$"
	cmd := exec.Command("go", "test", "-count=1", "-v", "-run", run, target.Package)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	exit := 0
	if ee, ok := runErr.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if runErr != nil {
		return nil, runErr
	}
	v := &contract.Verdict{
		Counterexample: cx.ID,
		PocDir:         pocDir,
		Evidence: contract.Evidence{
			Cmd:        "go test -count=1 -v -run '" + run + "' " + target.Package,
			Exit:       exit,
			StdoutTail: tail(stdout.String()),
			StderrTail: tail(stderr.String()),
		},
	}
	res, ok := parseResult(stdout.String())
	if !ok {
		v.Status = contract.VerdictInconclusive
		v.Diagnosis = &contract.Diagnosis{Reason: "harness_failed", Notes: []string{"no GO2LEAN_RESULT line; see stdout/stderr"}}
	} else {
		judge(v, prop, cx, res)
	}
	readme := fmt.Sprintf("# PoC %s\n\nProperty: %s (%s)\n\n%s\n\nVerdict: **%s**\n\nReproduce: copy `%s` into `%s` and run\n\n    %s\n",
		cx.ID, prop.ID, prop.Kind, prop.NL, v.Status, testName, pkgDir, v.Evidence.Cmd)
	if err := os.WriteFile(filepath.Join(pocDir, "README.md"), []byte(readme), 0o644); err != nil {
		return nil, err
	}
	return v, ws.WriteVerdict(v)
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

func parseResult(stdout string) (result, bool) {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "GO2LEAN_RESULT "); ok {
			var r result
			if json.Unmarshal([]byte(rest), &r) == nil {
				return r, true
			}
		}
	}
	return result{}, false
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
