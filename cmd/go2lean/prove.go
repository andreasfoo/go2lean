package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/pipeline"
	"github.com/andreasfoo/go2lean/internal/prover"
	"github.com/andreasfoo/go2lean/internal/repo"
)

// cmdProve states the functional theorems of a package's goals and asks
// Lean to prove them (issue #22).
func cmdProve(args []string) error {
	fs := flag.NewFlagSet("prove", flag.ExitOnError)
	ws := workspaceFlag(fs)
	goalsFile := fs.String("goals", "", `JSON file of goals, in order: [{"func": "Max2", "post": "∃ m, ret = .int 64 m ∧ a.sle m"}, ...]`)
	fn := fs.String("func", "", "one goal's function (with -post)")
	post := fs.String("post", "", "one goal's postcondition, a Lean proposition over the parameters, ret and s'")
	budget := fs.Duration("budget", 10*time.Minute, "time Lean may take on the file")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("prove: exactly one package expected")
	}
	var goals []prover.Goal
	switch {
	case *goalsFile != "":
		b, err := os.ReadFile(*goalsFile)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &goals); err != nil {
			return fmt.Errorf("prove: %s: %v", *goalsFile, err)
		}
	case *fn != "" && *post != "":
		goals = []prover.Goal{{Func: *fn, Post: *post}}
	default:
		return fmt.Errorf("prove: -goals, or -func with -post, expected")
	}
	w, env, err := openWorkspace(ws)
	if err != nil {
		return err
	}
	report, err := pipeline.Compile(w, env, fs.Arg(0), os.Stdout)
	if err != nil {
		return err
	}
	ids := map[string]int{}
	loops := map[string][]int{}
	for _, f := range report.Funcs {
		ids[f.Name] = f.ID
		loops[f.Name] = f.LoopHeaders
	}
	var stmts []string
	var fids []int
	for i, g := range goals {
		id, ok := ids[g.Func]
		if !ok {
			return fmt.Errorf("prove: %s is not a function of %s", g.Func, report.Package)
		}
		t, err := w.Target(contract.TargetID(report.Package, g.Func))
		if err != nil {
			return fmt.Errorf("prove: %s is not a target: %v", g.Func, err)
		}
		abi, err := w.ABI(t, report.Frontend)
		if err != nil {
			return err
		}
		st, err := prover.Statement(abi, id, prover.TheoremName(g.Func), g.Post)
		if err != nil {
			return fmt.Errorf("prove: %v", err)
		}
		stmts = append(stmts, st)
		fids = append(fids, id)
		if goals[i].Proof == "" && len(loops[g.Func]) > 0 {
			goals[i].Proof = prover.LoopProof(loops[g.Func])
		}
	}
	leanDir := repo.WorkspaceLeanDir(w.Root)
	rel, err := prover.FunctionalFile(leanDir, report, goals, stmts, fids)
	if err != nil {
		return err
	}
	fmt.Printf("theorems: %s\n", rel)
	rs, _, err := prover.CheckGoals(leanDir, rel, goals, *budget)
	if err != nil {
		return err
	}
	failed := 0
	for i, r := range rs {
		if r.Proved {
			fmt.Printf("  proved   %s (%s), kernel only\n", r.Func, r.Theorem)
		} else {
			failed++
			why := r.Error
			if h := loops[r.Func]; len(h) > 0 && strings.HasPrefix(goals[i].Proof, "  sorry") {
				why = fmt.Sprintf("loops at blocks %v need invariants", h)
			}
			fmt.Printf("  open     %s (%s): %s\n", r.Func, r.Theorem, why)
		}
	}
	if failed > 0 {
		return fmt.Errorf("prove: %d of %d goals not proved", failed, len(rs))
	}
	return nil
}
