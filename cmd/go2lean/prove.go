package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andreasfoo/go2lean/internal/agent"
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
	llmSpec := fs.String("llm", "", "agent for the goals left open (loops): claude, claude-code or mock:<file-or-dir>; empty: none")
	model := fs.String("model", "", "model id for the agent")
	outFile := fs.String("out", "", "write the goals with the proofs that worked (an agent's included) to this JSON file, to check again without the agent")
	rounds := fs.Int("rounds", 6, "agent attempts per goal, Lean's errors fed back after each")
	thinking := fs.Bool("thinking", true, "let the agent think at length (adaptive thinking; --effort high for claude-code)")
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
	var abis []*contract.ABI
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
		if _, err := prover.Statement(abi, id, prover.TheoremName(g.Func), g.Post); err != nil {
			return fmt.Errorf("prove: %v", err)
		}
		abis = append(abis, abi)
		fids = append(fids, id)
		if goals[i].Proof == "" && len(loops[g.Func]) > 0 {
			goals[i].Proof = prover.LoopProof(loops[g.Func])
		}
	}
	leanDir := repo.WorkspaceLeanDir(w.Root)
	file, err := prover.FunctionalFile(leanDir, report, goals, abis, fids)
	if err != nil {
		return err
	}
	fmt.Printf("theorems: %s\n", file.Rel)
	var rs []prover.GoalResult
	if *llmSpec == "" {
		if rs, _, err = prover.CheckGoals(leanDir, file, goals, *budget); err != nil {
			return err
		}
	} else {
		llm, err := agent.New(*llmSpec, agent.ClaudeConfig{Model: *model, Thinking: *thinking})
		if err != nil {
			return err
		}
		lib, err := proofLibrary(env.Root)
		if err != nil {
			return err
		}
		rs, err = agent.ProveGoals(context.Background(), llm, w, leanDir, report, goals, abis, fids,
			agent.ProveOptions{Rounds: *rounds, Budget: *budget, Library: lib}, os.Stdout)
		if err != nil {
			return err
		}
	}
	if *outFile != "" {
		// Only proved goals keep their proof text: an open one is written
		// as given, so that the file states what is proved.
		saved := make([]prover.Goal, len(goals))
		for i, g := range goals {
			saved[i] = g
			if !rs[i].Proved || strings.HasPrefix(g.Proof, "  sorry") {
				saved[i].Prelude, saved[i].Proof = "", ""
			}
		}
		b, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*outFile, append(b, '\n'), 0o644); err != nil {
			return err
		}
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

// proofLibrary is what the proof agent reads before its goal: the
// weakest-precondition library, its tactics and lemmas, and the hand
// proofs as worked examples.
func proofLibrary(root string) (string, error) {
	var b strings.Builder
	for _, f := range []string{"lean/Go2Lean/Proof/WP.lean", "lean/Go2Lean/Proof/Tactic.lean",
		"lean/Go2Lean/Proof/Simp.lean", "lean/Proofs/Functional.lean"} {
		text, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "## %s\n\n```lean\n%s```\n\n", f, text)
	}
	return b.String(), nil
}
