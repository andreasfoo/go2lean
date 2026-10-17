package main

import (
	"flag"
	"fmt"

	"github.com/andreasfoo/go2lean/internal/constraint"
	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/poc"
)

func searchFlags(fs *flag.FlagSet) *constraint.SearchOptions {
	o := &constraint.SearchOptions{}
	fs.IntVar(&o.Seed, "seed", 42, "random seed")
	fs.IntVar(&o.Trials, "trials", 2000, "random trials per property")
	fs.IntVar(&o.Fuel, "fuel", 1000, "interpreter fuel (block entries + calls)")
	fs.IntVar(&o.MaxLen, "maxlen", 8, "maximum generated slice length")
	return o
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
	props, err := constraint.Propose(w, report)
	if err != nil {
		return err
	}
	for _, p := range props {
		fmt.Printf("  %-28s %-14s %s\n", p.ID, p.Kind, p.NL)
	}
	return nil
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
		if _, err := hunt(w, root, p, *opts); err != nil {
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

func hunt(w contract.Workspace, root string, p *contract.Property, opts constraint.SearchOptions) (*contract.Counterexample, error) {
	cx, summary, err := constraint.Hunt(w, root, p, opts)
	if err != nil {
		return nil, fmt.Errorf("hunt %s: %w", p.ID, err)
	}
	if cx == nil {
		fmt.Printf("  %-28s %s\n", p.ID, summary)
		return nil, nil
	}
	fmt.Printf("  %-28s %s -> %s\n", p.ID, summary, cx.ID)
	fmt.Printf("      inputs %v", cx.Inputs)
	if len(cx.Memory) > 0 {
		fmt.Printf(" memory %v", cx.Memory)
	}
	if cx.ModelOutput != nil {
		fmt.Printf(" model %s %s %s", cx.ModelOutput.Res, cx.ModelOutput.Value, cx.ModelOutput.Panic)
	}
	fmt.Println()
	return cx, nil
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
	return verify(w, root, *id)
}

func verify(w contract.Workspace, root, id string) error {
	v, err := poc.Verify(w, root, id)
	if err != nil {
		return fmt.Errorf("poc %s: %w", id, err)
	}
	fmt.Printf("  %-28s %-12s", id, v.Status)
	if v.Observed != nil {
		fmt.Printf(" real: %v", v.Observed)
	}
	if v.Diagnosis != nil {
		fmt.Printf(" (%s)", v.Diagnosis.Reason)
	}
	fmt.Println()
	if v.Status == contract.VerdictConfirmed {
		fmt.Printf("      PoC: %s\n", v.PocDir)
	}
	return nil
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
	fmt.Println("== M1 compile")
	report, err := compile(w, root, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Println("== M2 properties")
	props, err := constraint.Propose(w, report)
	if err != nil {
		return err
	}
	for _, p := range props {
		fmt.Printf("  %-28s %-14s %s\n", p.ID, p.Kind, p.NL)
	}
	fmt.Println("== M2 hunt")
	var cxs []*contract.Counterexample
	for _, p := range props {
		cx, err := hunt(w, root, p, *opts)
		if err != nil {
			return err
		}
		if cx != nil {
			cxs = append(cxs, cx)
		}
	}
	fmt.Println("== M3 poc")
	if len(cxs) == 0 {
		fmt.Println("  nothing to verify")
	}
	for _, cx := range cxs {
		if err := verify(w, root, cx.ID); err != nil {
			return err
		}
	}
	return nil
}
