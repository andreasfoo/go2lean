package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andreasfoo/go2lean/internal/constraint"
	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/pipeline"
)

// SpecFile is the file the agent owns inside the target package, unless
// Options.SpecFile names another (to sit next to hand-written specs).
const SpecFile = "go2lean_specs_test.go"

// Options tune a run.
type Options struct {
	Search      constraint.SearchOptions
	MaxRepairs  int // rounds spent fixing specs the frontend rejected
	ReportRound bool
	// Guidance is extra text for the proposal round: known bug classes,
	// invariants from the issue tracker, functions to focus on.
	Guidance string
	// SpecFile overrides the name of the file the agent writes (default
	// SpecFile); it must end in _test.go.
	SpecFile string
}

// Run drives the agent on one package: propose specs, repair until they
// model, run the pipeline, then (optionally) ask for a report and a revised
// spec file and run once more.
func Run(ctx context.Context, ws contract.Workspace, env pipeline.Env, pattern string, llm LLM, opts Options, out io.Writer) error {
	if opts.MaxRepairs == 0 {
		opts.MaxRepairs = 2
	}
	specFile := SpecFile
	if opts.SpecFile != "" {
		if !strings.HasSuffix(opts.SpecFile, "_test.go") {
			return fmt.Errorf("agent: spec file %s must end in _test.go", opts.SpecFile)
		}
		specFile = opts.SpecFile
	}
	moduleDir := env.Module
	if moduleDir == "" {
		moduleDir = env.Root
	}
	pkgDir, pkgName, pkgPath, err := goList(moduleDir, pattern)
	if err != nil {
		return err
	}
	// One log directory per package, so runs on different packages of the
	// same workspace keep their prompts, responses and reports.
	logDir := filepath.Join(ws.Root, "agent", contract.PackageKey(pkgPath))
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	call := 0
	ask := func(system, user string) (string, error) {
		call++
		_ = os.WriteFile(filepath.Join(logDir, fmt.Sprintf("call-%d-prompt.md", call)), []byte(system+"\n\n---\n\n"+user), 0o644)
		fmt.Fprintf(out, "== agent (%s) call %d\n", llm.Name(), call)
		resp, err := llm.Complete(ctx, system, user)
		if err != nil {
			return "", err
		}
		_ = os.WriteFile(filepath.Join(logDir, fmt.Sprintf("call-%d-response.md", call)), []byte(resp), 0o644)
		return resp, nil
	}

	fmt.Fprintln(out, "== M1 compile (before specs)")
	report, err := pipeline.Compile(ws, env, pattern, out)
	if err != nil {
		return err
	}
	source, err := packageSource(pkgDir)
	if err != nil {
		return err
	}

	// install writes a spec file into the package and compiles, sending
	// build errors and rejected specs back to the model until everything is
	// modelled or the repair budget is spent. It returns the installed spec.
	install := func(spec string) (string, *contract.ModelReport, error) {
		for repair := 0; ; repair++ {
			spec = normalizeSpec(spec, pkgName)
			if err := os.WriteFile(filepath.Join(pkgDir, specFile), []byte(spec), 0o644); err != nil {
				return "", nil, err
			}
			fmt.Fprintf(out, "== M1 compile (specs, attempt %d)\n", repair+1)
			report, err := pipeline.Compile(ws, env, pattern, out)
			var issues []string
			if err != nil {
				// A spec that does not even type-check is an issue for the agent, not a crash.
				issues = []string{"go build: " + err.Error()}
			} else {
				issues = specIssues(report)
			}
			if len(issues) == 0 {
				_ = os.WriteFile(filepath.Join(logDir, specFile), []byte(spec), 0o644)
				return spec, report, nil
			}
			if repair >= opts.MaxRepairs {
				if err != nil {
					return "", nil, fmt.Errorf("specs still do not compile after %d repairs: %w", repair, err)
				}
				_ = os.WriteFile(filepath.Join(logDir, specFile), []byte(spec), 0o644)
				return spec, report, nil // rejected specs are reported, not fatal
			}
			resp, err := ask(systemPrompt, repairPrompt(issues, spec))
			if err != nil {
				return "", nil, err
			}
			if next := extractGo(resp); next != "" {
				spec = next
			}
		}
	}

	resp, err := ask(systemPrompt, proposePrompt(pkgPath, pkgName, source, report)+guidanceSection(opts.Guidance))
	if err != nil {
		return err
	}
	spec := extractGo(resp)
	if spec == "" {
		return fmt.Errorf("agent response contained no Go code block")
	}
	spec, report, err = install(spec)
	if err != nil {
		return err
	}

	res, err := runRest(ws, env, report, opts.Search, out)
	if err != nil {
		return err
	}
	if !opts.ReportRound {
		return nil
	}

	// Report round: source and verdicts back to the agent; a revised spec
	// file triggers one more pass.
	resp, err = ask(reportPrompt, reportUserPrompt(pkgPath, pkgName, source, verdictSummary(ws, res)))
	if err != nil {
		return err
	}
	reportPath := filepath.Join(logDir, "report.md")
	if err := os.WriteFile(reportPath, []byte(stripGo(resp)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "== report written to %s\n", reportPath)
	if revised := extractGo(resp); hasSpecBlock(resp) && normalizeSpec(revised, pkgName) != spec {
		fmt.Fprintln(out, "== agent revised the specs; running again")
		if _, report, err = install(revised); err != nil {
			return err
		}
		if _, err := runRest(ws, env, report, opts.Search, out); err != nil {
			return err
		}
	}
	return nil
}

func runRest(ws contract.Workspace, env pipeline.Env, report *contract.ModelReport, so constraint.SearchOptions, out io.Writer) (*pipeline.Result, error) {
	fmt.Fprintln(out, "== M2 properties")
	props, err := pipeline.Propose(ws, report, out)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "== M2 hunt")
	res := &pipeline.Result{Report: report, Properties: props}
	var cxs []*contract.Counterexample
	for _, p := range props {
		cx, err := pipeline.Hunt(ws, env, p, so, out)
		if err != nil {
			return nil, err
		}
		if cx != nil {
			cxs = append(cxs, cx)
		}
	}
	fmt.Fprintln(out, "== M3 poc")
	if len(cxs) == 0 {
		fmt.Fprintln(out, "  nothing to verify")
	}
	for _, cx := range cxs {
		v, err := pipeline.Verify(ws, env, cx.ID, out)
		if err != nil {
			return nil, err
		}
		res.Verdicts = append(res.Verdicts, v)
	}
	return res, nil
}

// specIssues lists the specs the frontend rejected.
func specIssues(r *contract.ModelReport) []string {
	var out []string
	for _, f := range r.Funcs {
		if f.SpecFor != "" && f.Status == contract.ModelRejected {
			out = append(out, fmt.Sprintf("%s (spec for %s): %s", f.Name, f.SpecFor, strings.Join(f.Issues, "; ")))
		}
	}
	return out
}

// verdictSummary renders what the pipeline found for the report prompt.
func verdictSummary(ws contract.Workspace, res *pipeline.Result) string {
	var b strings.Builder
	b.WriteString("Properties:\n")
	for _, p := range res.Properties {
		fmt.Fprintf(&b, "- %s [%s]: %s\n", p.ID, p.Kind, p.NL)
	}
	b.WriteString("\nVerdicts:\n")
	byProp := map[string]bool{}
	for _, v := range res.Verdicts {
		cx, err := ws.Counterexample(v.Counterexample)
		if err != nil {
			continue
		}
		byProp[cx.Property] = true
		fmt.Fprintf(&b, "- %s on %s: %s\n    inputs %v", v.Counterexample, cx.Property, v.Status, cx.Inputs)
		if len(cx.Memory) > 0 {
			fmt.Fprintf(&b, " memory %v", cx.Memory)
		}
		if cx.ModelOutput != nil {
			fmt.Fprintf(&b, "\n    model: %s %s %s", cx.ModelOutput.Res, cx.ModelOutput.Value, cx.ModelOutput.Panic)
		}
		fmt.Fprintf(&b, "\n    real: %v", v.Observed)
		if v.Diagnosis != nil {
			fmt.Fprintf(&b, "\n    diagnosis: %s %s", v.Diagnosis.Reason, v.Diagnosis.ModelVsReal)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nProperties without a counterexample:\n")
	for _, p := range res.Properties {
		if byProp[p.ID] {
			continue
		}
		status := "no counterexample found by random search"
		if pr, err := readProof(ws, p.ID); err == nil {
			status = fmt.Sprintf("%s: %s", pr.Status, pr.Domain)
		}
		fmt.Fprintf(&b, "- %s: %s\n", p.ID, status)
	}
	return b.String()
}

func readProof(ws contract.Workspace, propID string) (*contract.ProofResult, error) {
	var pr contract.ProofResult
	b, err := os.ReadFile(filepath.Join(ws.Root, "proofs", propID+".json"))
	if err != nil {
		return nil, err
	}
	return &pr, unmarshal(b, &pr)
}

// stripGo replaces the spec-file block (recognised by its go2lean:spec
// comments) with a note, leaving fix snippets and other code in the report.
func stripGo(resp string) string {
	out := goBlock.ReplaceAllStringFunc(resp, func(block string) string {
		if strings.Contains(block, "go2lean:spec") {
			return "(revised spec file written to go2lean_specs_test.go)"
		}
		return block
	})
	return strings.TrimSpace(out) + "\n"
}

func goList(root, pattern string) (dir, name, path string, err error) {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}\t{{.Name}}\t{{.ImportPath}}", pattern)
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return "", "", "", fmt.Errorf("go list %s: %w", pattern, err)
	}
	parts := strings.Split(strings.TrimSpace(string(b)), "\t")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("go list %s: unexpected output %q", pattern, b)
	}
	return parts[0], parts[1], parts[2], nil
}
