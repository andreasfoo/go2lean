// Package pipeline composes the three modules into the steps the CLI and the
// agent both run: compile (M1), propose and hunt (M2), verify (M3).
package pipeline

import (
	"fmt"
	"io"

	"github.com/andreasfoo/go2lean/internal/constraint"
	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/frontend/gossa"
	"github.com/andreasfoo/go2lean/internal/poc"
	"github.com/andreasfoo/go2lean/internal/repo"
	"github.com/andreasfoo/go2lean/internal/ssaout"
)

// Compile runs M1 on one package and prints a summary to out.
func Compile(w contract.Workspace, root, pattern string, out io.Writer) (*contract.ModelReport, error) {
	prog, modPath, err := gossa.Translate(root, pattern)
	if err != nil {
		return nil, err
	}
	report, err := ssaout.Emit(prog, repo.LeanDir(root), w, modPath)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "compiled %s -> %s\n", prog.Package, report.LeanFile)
	for _, f := range report.Funcs {
		fmt.Fprintf(out, "  %-12s %-9s %s", f.Name, f.Status, f.GoSig)
		if f.SpecFor != "" {
			fmt.Fprintf(out, "  [spec for %s]", f.SpecFor)
		}
		fmt.Fprintln(out)
		for _, is := range f.Issues {
			fmt.Fprintf(out, "      - %s\n", is)
		}
	}
	return report, nil
}

// Propose runs M2's property templates and prints them.
func Propose(w contract.Workspace, report *contract.ModelReport, out io.Writer) ([]*contract.Property, error) {
	props, err := constraint.Propose(w, report)
	if err != nil {
		return nil, err
	}
	for _, p := range props {
		fmt.Fprintf(out, "  %-28s %-14s %s\n", p.ID, p.Kind, p.NL)
	}
	return props, nil
}

// Hunt runs the engines on one property and prints the outcome.
func Hunt(w contract.Workspace, root string, p *contract.Property, opts constraint.SearchOptions, out io.Writer) (*contract.Counterexample, error) {
	cx, summary, err := constraint.Hunt(w, root, p, opts)
	if err != nil {
		return nil, fmt.Errorf("hunt %s: %w", p.ID, err)
	}
	if cx == nil {
		fmt.Fprintf(out, "  %-28s %s\n", p.ID, summary)
		return nil, nil
	}
	fmt.Fprintf(out, "  %-28s %s -> %s\n", p.ID, summary, cx.ID)
	fmt.Fprintf(out, "      inputs %v", cx.Inputs)
	if len(cx.Memory) > 0 {
		fmt.Fprintf(out, " memory %v", cx.Memory)
	}
	if cx.ModelOutput != nil {
		fmt.Fprintf(out, " model %s %s %s", cx.ModelOutput.Res, cx.ModelOutput.Value, cx.ModelOutput.Panic)
	}
	fmt.Fprintln(out)
	return cx, nil
}

// Verify runs M3 on one counterexample and prints the verdict.
func Verify(w contract.Workspace, root, id string, out io.Writer) (*contract.Verdict, error) {
	v, err := poc.Verify(w, root, id)
	if err != nil {
		return nil, fmt.Errorf("poc %s: %w", id, err)
	}
	fmt.Fprintf(out, "  %-28s %-12s", id, v.Status)
	if v.Observed != nil {
		fmt.Fprintf(out, " real: %v", v.Observed)
	}
	if v.Diagnosis != nil {
		fmt.Fprintf(out, " (%s)", v.Diagnosis.Reason)
	}
	fmt.Fprintln(out)
	if v.Status == contract.VerdictConfirmed {
		fmt.Fprintf(out, "      PoC: %s\n", v.PocDir)
	}
	return v, nil
}

// Result is what one full pass over a package produced.
type Result struct {
	Report     *contract.ModelReport
	Properties []*contract.Property
	Verdicts   []*contract.Verdict
}

// Run executes all three modules on one package.
func Run(w contract.Workspace, root, pattern string, opts constraint.SearchOptions, out io.Writer) (*Result, error) {
	fmt.Fprintln(out, "== M1 compile")
	report, err := Compile(w, root, pattern, out)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "== M2 properties")
	props, err := Propose(w, report, out)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "== M2 hunt")
	var cxs []*contract.Counterexample
	for _, p := range props {
		cx, err := Hunt(w, root, p, opts, out)
		if err != nil {
			return nil, err
		}
		if cx != nil {
			cxs = append(cxs, cx)
		}
	}
	fmt.Fprintln(out, "== M3 poc")
	res := &Result{Report: report, Properties: props}
	if len(cxs) == 0 {
		fmt.Fprintln(out, "  nothing to verify")
	}
	for _, cx := range cxs {
		v, err := Verify(w, root, cx.ID, out)
		if err != nil {
			return nil, err
		}
		res.Verdicts = append(res.Verdicts, v)
	}
	return res, nil
}
