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
	return &engineOptions{o, engine}
}

func cmdProps(args []string) error {
	fs := flag.NewFlagSet("props", flag.ExitOnError)
	ws := workspaceFlag(fs)
	frontend := fs.String("frontend", "gossa", "frontend whose model report to use")
	fs.Parse(args)
	w, _, err := openWorkspace(*ws)
	if err != nil {
		return err
	}
	report, err := w.ModelReport(*frontend)
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
	w, root, err := openWorkspace(*ws)
	if err != nil {
		return err
	}
	props, err := selectProps(w, *id)
	if err != nil {
		return err
	}
	for _, p := range props {
		if _, err := pipeline.Hunt(w, root, p, opts.get(), os.Stdout); err != nil {
			return err
		}
	}
	return nil
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
	id := fs.String("counterexample", "", "counterexample id")
	fs.Parse(args)
	if *id == "" {
		return fmt.Errorf("poc: -counterexample is required")
	}
	w, root, err := openWorkspace(*ws)
	if err != nil {
		return err
	}
	_, err = pipeline.Verify(w, root, *id, os.Stdout)
	return err
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	ws := workspaceFlag(fs)
	opts := searchFlags(fs)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("run: exactly one package pattern expected")
	}
	w, root, err := openWorkspace(*ws)
	if err != nil {
		return err
	}
	_, err = pipeline.Run(w, root, fs.Arg(0), opts.get(), os.Stdout)
	return err
}
