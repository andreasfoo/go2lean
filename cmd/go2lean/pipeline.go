package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/andreasfoo/go2lean/internal/constraint"
	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/pipeline"
)

// engineOptions defers reading the -engine flag until after Parse.
type engineOptions struct {
	*constraint.SearchOptions
	engine *string
}

func (e *engineOptions) get() constraint.SearchOptions {
	e.Engine = constraint.Engine(*e.engine)
	return *e.SearchOptions
}

func searchFlags(fs *flag.FlagSet) *engineOptions {
	o := &constraint.SearchOptions{}
	engine := fs.String("engine", "all", "engine: random, exhaustive or all")
	fs.IntVar(&o.Seed, "seed", 42, "random seed")
	fs.IntVar(&o.Trials, "trials", 2000, "random trials per property")
	fs.IntVar(&o.Fuel, "fuel", 1000, "interpreter fuel (block entries + calls)")
	fs.IntVar(&o.MaxLen, "maxlen", 8, "maximum generated slice length")
	fs.IntVar(&o.ExhaustLen, "exhaust-len", 3, "maximum slice length in the exhaustive engine")
	fs.IntVar(&o.ExhaustLimit, "exhaust-limit", 300000, "maximum domain size the exhaustive engine attempts")
	fs.BoolVar(&o.NilInputs, "nil-inputs", false, "let top-level pointer arguments and receivers be nil")
	fs.IntVar(&o.Jobs, "jobs", 0, "properties searched in parallel (default: number of CPUs)")
	fs.IntVar(&o.BudgetSeconds, "budget", 10, "seconds each engine may spend on one property (0: unlimited)")
	return &engineOptions{o, engine}
}

func cmdProps(args []string) error {
	fs := flag.NewFlagSet("props", flag.ExitOnError)
	ws := workspaceFlag(fs)
	frontend := fs.String("frontend", "gossa", "frontend whose model report to use")
	pkg := fs.String("package", "", "import path of the compiled package")
	fs.Parse(args)
	if *pkg == "" {
		return fmt.Errorf("props: -package is required")
	}
	w, _, err := openWorkspace(ws)
	if err != nil {
		return err
	}
	report, err := w.ModelReport(*frontend, *pkg)
	if err != nil {
		return err
	}
	_, err = pipeline.Propose(w, report, os.Stdout)
	return err
}

func cmdHunt(args []string) error {
	fs := flag.NewFlagSet("hunt", flag.ExitOnError)
	ws := workspaceFlag(fs)
	id := fs.String("property", "", "property id (default: all)")
	opts := searchFlags(fs)
	fs.Parse(args)
	w, env, err := openWorkspace(ws)
	if err != nil {
		return err
	}
	props, err := selectProps(w, *id)
	if err != nil {
		return err
	}
	_, err = pipeline.HuntAll(w, env, props, opts.get(), os.Stdout)
	return err
}

func selectProps(w contract.Workspace, id string) ([]*contract.Property, error) {
	if id != "" {
		p, err := w.Property(id)
		return []*contract.Property{p}, err
	}
	return w.Properties()
}

func cmdPoc(args []string) error {
	fs := flag.NewFlagSet("poc", flag.ExitOnError)
	ws := workspaceFlag(fs)
	id := fs.String("counterexample", "", "counterexample id (more ids may follow as arguments)")
	fs.Parse(args)
	ids := fs.Args()
	if *id != "" {
		ids = append([]string{*id}, ids...)
	}
	if len(ids) == 0 {
		return fmt.Errorf("poc: at least one counterexample id is required")
	}
	w, env, err := openWorkspace(ws)
	if err != nil {
		return err
	}
	_, err = pipeline.VerifyAll(w, env, ids, os.Stdout)
	return err
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	ws := workspaceFlag(fs)
	opts := searchFlags(fs)
	deps := fs.Bool("deps", false, "also model the functions of dependency packages the compiled packages call")
	resume := fs.Bool("resume", false, "skip properties already searched and counterexamples already verified in the workspace")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("run: at least one package pattern expected (e.g. ./...)")
	}
	w, env, err := openWorkspace(ws)
	if err != nil {
		return err
	}
	env.Deps, env.Resume = *deps, *resume
	_, err = pipeline.RunAll(w, env, opts.get(), os.Stdout, fs.Args()...)
	return err
}
