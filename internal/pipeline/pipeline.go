// Package pipeline composes the three modules into the steps the CLI and the
// agent both run: compile (M1), propose and hunt (M2), verify (M3).
package pipeline

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/andreasfoo/go2lean/internal/constraint"
	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/frontend/gossa"
	"github.com/andreasfoo/go2lean/internal/fuzzimport"
	"github.com/andreasfoo/go2lean/internal/poc"
	"github.com/andreasfoo/go2lean/internal/repo"
	"github.com/andreasfoo/go2lean/internal/ssaout"
)

// Env says where things are: Root is the go2lean checkout (Lean project),
// Module is the directory of the Go module under analysis (defaults to Root).
type Env struct {
	Root   string
	Module string
	// Deps models the dependency functions the compiled packages call
	// (see gossa.Options.Deps).
	Deps bool
	// Resume skips properties whose search is recorded in the workspace and
	// counterexamples that already have a verdict.
	Resume bool
	// FuzzSpecs reads the packages' Go fuzz targets as sequence specs
	// (internal/fuzzimport); their generated file joins Overlay.
	FuzzSpecs bool
	// Overlay adds files to the packages for compiling and replaying
	// (absolute path to contents), never written into them.
	Overlay map[string][]byte
	// Tests makes test functions targets (gossa.Options.Tests).
	Tests bool
}

func (e Env) module() string {
	if e.Module != "" {
		return e.Module
	}
	return e.Root
}

// Compile runs M1 on one package and prints a summary to out.
func Compile(w contract.Workspace, env Env, pattern string, out io.Writer) (*contract.ModelReport, error) {
	all, err := CompileAll(w, env, out, pattern)
	if err != nil {
		return nil, err
	}
	// The dependencies -deps models come along; the pattern names one.
	var reports []*contract.ModelReport
	for _, r := range all {
		if !r.Dependency {
			reports = append(reports, r)
		}
	}
	if len(reports) != 1 {
		return nil, fmt.Errorf("pattern %s matched %d packages; expected exactly one", pattern, len(reports))
	}
	return reports[0], nil
}

// CompileAll runs M1 on every package matching the patterns (e.g. "./...")
// with a single load of the dependency graph.
func CompileAll(w contract.Workspace, env Env, out io.Writer, patterns ...string) ([]*contract.ModelReport, error) {
	progs, modPath, err := gossa.TranslateAllOpts(env.module(), gossa.Options{Deps: env.Deps, Overlay: env.Overlay, Tests: env.Tests}, patterns...)
	if err != nil {
		return nil, err
	}
	var reports []*contract.ModelReport
	leanDir := repo.WorkspaceLeanDir(w.Root)
	core, err := repo.CoreDir(env.Root)
	if err != nil {
		return nil, err
	}
	if err := ssaout.EnsureProject(leanDir, core); err != nil {
		return nil, err
	}
	for _, prog := range progs {
		constraint.InvalidateDriver()
		report, err := ssaout.Emit(prog, leanDir, w, modPath)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "compiled %s -> %s\n", prog.Package, filepath.Join(w.Root, report.LeanFile))
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
		reports = append(reports, report)
	}
	return reports, nil
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
func Hunt(w contract.Workspace, env Env, p *contract.Property, opts constraint.SearchOptions, out io.Writer) (*contract.Counterexample, error) {
	if env.Resume {
		if r := w.HuntRecord(p.ID); r != nil {
			fmt.Fprintf(out, "  %-28s (resumed) %s\n", p.ID, r.Summary)
			if r.Counterexample == "" {
				return nil, nil
			}
			return w.Counterexample(r.Counterexample)
		}
	}
	cx, summary, err := constraint.Hunt(w, env.Root, p, opts)
	if err != nil {
		return nil, fmt.Errorf("hunt %s: %w", p.ID, err)
	}
	rec := &contract.HuntRecord{Property: p.ID, Summary: summary}
	if cx != nil {
		rec.Counterexample = cx.ID
	}
	if err := w.WriteHuntRecord(rec); err != nil {
		return nil, err
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
	for _, pr := range cx.Probes {
		fmt.Fprintf(out, "      probe %s = %s\n", pr.Label, pr.Value)
	}
	return cx, nil
}

// HuntAll runs Hunt on every property, opts.Jobs at a time, printing each
// property's lines together in the order given.
func HuntAll(w contract.Workspace, env Env, props []*contract.Property, opts constraint.SearchOptions, out io.Writer) ([]*contract.Counterexample, error) {
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	type result struct {
		cx  *contract.Counterexample
		err error
		log bytes.Buffer
	}
	results := make([]result, len(props))
	var wg sync.WaitGroup
	sem := make(chan struct{}, jobs)
	for i, p := range props {
		wg.Add(1)
		go func(i int, p *contract.Property) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i].cx, results[i].err = Hunt(w, env, p, opts, &results[i].log)
		}(i, p)
	}
	wg.Wait()
	var cxs []*contract.Counterexample
	for i := range results {
		io.Copy(out, &results[i].log)
		if results[i].err != nil {
			return cxs, results[i].err
		}
		if results[i].cx != nil {
			cxs = append(cxs, results[i].cx)
		}
	}
	return cxs, nil
}

// Verify runs M3 on one counterexample and prints the verdict.
func Verify(w contract.Workspace, env Env, id string, out io.Writer) (*contract.Verdict, error) {
	vs, err := VerifyAll(w, env, []string{id}, out)
	if err != nil {
		return nil, err
	}
	return vs[0], nil
}

// VerifyAll runs M3 on several counterexamples (one go test per package).
func VerifyAll(w contract.Workspace, env Env, ids []string, out io.Writer) ([]*contract.Verdict, error) {
	var done []*contract.Verdict
	if env.Resume {
		var todo []string
		for _, id := range ids {
			if v := w.Verdict(id); v != nil {
				done = append(done, v)
			} else {
				todo = append(todo, id)
			}
		}
		ids = todo
	}
	var vs []*contract.Verdict
	if len(ids) > 0 {
		var err error
		vs, err = poc.VerifyAll(w, env.module(), ids, env.Overlay)
		if err != nil {
			return nil, fmt.Errorf("poc: %w", err)
		}
	}
	vs = append(done, vs...)
	for _, v := range vs {
		printVerdict(v, out)
	}
	return vs, nil
}

func printVerdict(v *contract.Verdict, out io.Writer) {
	id := v.Counterexample
	fmt.Fprintf(out, "  %-28s %-12s", id, v.Status)
	if v.Observed != nil {
		fmt.Fprintf(out, " real: %v", v.Observed)
	}
	if v.Diagnosis != nil {
		fmt.Fprintf(out, " (%s)", v.Diagnosis.Reason)
	}
	fmt.Fprintln(out)
	if v.Status == contract.VerdictSpurious && v.Diagnosis != nil {
		for _, n := range v.Diagnosis.Notes {
			fmt.Fprintf(out, "      %s\n", n)
		}
	}
	if v.Status == contract.VerdictConfirmed {
		if v.Evidence.PanicSite != "" {
			fmt.Fprintf(out, "      at %s\n", v.Evidence.PanicSite)
		}
		if v.Rank != nil {
			fmt.Fprintf(out, "      rank %d: %s\n", v.Rank.Tier, v.Rank.Reason)
			for _, s := range v.Rank.Sites {
				fmt.Fprintf(out, "        %s\n", s)
			}
		}
		fmt.Fprintf(out, "      PoC: %s\n", v.PocDir)
	}
	for _, pr := range v.Probes {
		fmt.Fprintf(out, "      real probe %s = %s\n", pr.Label, pr.Value)
	}
}

// Result is what one full pass over a package produced.
type Result struct {
	Report     *contract.ModelReport
	Properties []*contract.Property
	Verdicts   []*contract.Verdict
}

// Run executes all three modules on one package.
func Run(w contract.Workspace, env Env, pattern string, opts constraint.SearchOptions, out io.Writer) (*Result, error) {
	results, err := RunAll(w, env, opts, out, pattern)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("pattern %s matched %d packages; expected exactly one", pattern, len(results))
	}
	return results[0], nil
}

// RunAll executes all three modules on every package matching the patterns
// with a single load of the module, skipping packages with nothing modelled.
func RunAll(w contract.Workspace, env Env, opts constraint.SearchOptions, out io.Writer, patterns ...string) ([]*Result, error) {
	if env.FuzzSpecs {
		overlay, skipped, err := fuzzimport.Specs(env.module(), patterns...)
		if err != nil {
			return nil, err
		}
		if len(overlay) > 0 || len(skipped) > 0 {
			fmt.Fprintln(out, "== fuzz targets as specs")
		}
		for path, src := range overlay {
			fmt.Fprintf(out, "  %s: %d specs\n", filepath.Dir(path), strings.Count(string(src), "go2lean:sequence"))
		}
		for _, s := range skipped {
			fmt.Fprintf(out, "  %s.%s not read: %s\n", s.Package, s.Fuzz, s.Reason)
		}
		env.Overlay = maps.Clone(env.Overlay)
		if env.Overlay == nil {
			env.Overlay = map[string][]byte{}
		}
		maps.Copy(env.Overlay, overlay)
	}
	fmt.Fprintln(out, "== M1 compile")
	reports, err := CompileAll(w, env, out, patterns...)
	if err != nil {
		return nil, err
	}
	var results []*Result
	for _, report := range reports {
		if report.Dependency {
			// Modelled for its callers (-deps); not a target of the run.
			continue
		}
		modelled := false
		for _, f := range report.Funcs {
			if f.Status != contract.ModelRejected {
				modelled = true
			}
		}
		if !modelled {
			continue
		}
		fmt.Fprintf(out, "######## %s\n", report.Package)
		res, err := runReport(w, env, report, opts, out)
		if err != nil {
			// One package's trouble (a Lean build failure, a harness that does
			// not compile) must not end a sweep.
			fmt.Fprintf(out, "  error: %v\n", err)
			continue
		}
		results = append(results, res)
	}
	return results, nil
}

func runReport(w contract.Workspace, env Env, report *contract.ModelReport, opts constraint.SearchOptions, out io.Writer) (*Result, error) {
	fmt.Fprintln(out, "== M2 properties")
	props, err := Propose(w, report, out)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "== M2 hunt")
	cxs, err := HuntAll(w, env, props, opts, out)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "== M3 poc")
	res := &Result{Report: report, Properties: props}
	if len(cxs) == 0 {
		fmt.Fprintln(out, "  nothing to verify")
		return res, nil
	}
	var ids []string
	for _, cx := range cxs {
		ids = append(ids, cx.ID)
	}
	vs, err := VerifyAll(w, env, ids, out)
	if err != nil {
		return nil, err
	}
	res.Verdicts = vs
	return res, nil
}
