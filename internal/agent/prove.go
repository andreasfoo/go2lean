package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/prover"
)

// ProveOptions configure the proof agent.
type ProveOptions struct {
	Rounds int           // attempts per goal, Lean's errors fed back after each (default 4)
	Budget time.Duration // Lean's time on the file, per round
	// Library is the text of the proof library and the worked examples the
	// prompt carries (WP.lean, Tactic.lean, the hand proofs).
	Library string
}

const proveSystem = `You are the proof agent of go2lean, which translates Go functions into a
deep embedding in Lean 4 (v4.35, core and Std only: no Mathlib) and proves
functional properties of them over that model.

go2lean has stated a theorem: the contract of one Go function, in the form
wpFun prog or0 h0 <id> [args] (fun ret s' => <postcondition>) s, meaning that
every run of the function on these arguments that ends returns without
panicking, with a result ret and final state s' satisfying the
postcondition (partial correctness). You write its proof. The function has
loops: each needs an invariant at its header block, applied with
wpPt_loop, as in the worked examples.

Answer with exactly two fenced blocks and nothing that matters outside them:

` + "```lean prelude" + `
-- definitions and lemmas the proof uses (invariants, helper lemmas);
-- may be empty
` + "```" + `

` + "```lean proof" + `
-- the tactic block that proves the theorem (what follows ":= by")
` + "```" + `

How a proof goes (the worked examples do exactly this): apply
wpFun_of_wpPt prog_fN, walk the blocks before the loop, then apply
wpPtN_of_wpPt and wpPt_loop prog_fN <invariant> <step lemma>. In the step,
walk every block of each path from the header, one wp_step per block
(branches split with cases on the condition), until the path either
returns (prove the postcondition) or jumps back to the header (close with
wpPtN_le and the induction hypothesis, then prove the invariant again).
Missing a block on a path is the most common error: the goal then still
shows a match on the next block's instructions. The lookup equation
prog_fN is already stated; do not restate it.

Rules: the statement is fixed and not yours to write or restate. Never use
sorry, admit, axiom, native_decide, macros, notation, syntax extensions,
namespace or section commands, or anything that runs code at elaboration.
The kernel checks the result, and #print axioms must list no more than
propext, Classical.choice and Quot.sound. Prefer small lemmas in the
prelude over one long proof. When Lean's errors come back, fix the cause;
the goal states in the errors show what is left to prove.`

// ProveGoals asks the LLM for proofs of the goals Lean did not prove,
// one goal at a time in order, up to Rounds attempts each with Lean's
// errors fed back. goals are updated in place with the proofs that
// worked; a goal that stays open gets its original proof back, so that
// the goals after it are checked as before.
func ProveGoals(ctx context.Context, llm LLM, ws contract.Workspace, leanDir string, report *contract.ModelReport,
	goals []prover.Goal, abis []*contract.ABI, fids []int, opts ProveOptions, out io.Writer) ([]prover.GoalResult, error) {
	if opts.Rounds <= 0 {
		opts.Rounds = 4
	}
	if opts.Budget <= 0 {
		opts.Budget = 10 * time.Minute
	}
	check := func() ([]prover.GoalResult, *prover.File, string, error) {
		f, err := prover.FunctionalFile(leanDir, report, goals, abis, fids)
		if err != nil {
			return nil, nil, "", err
		}
		rs, text, err := prover.CheckGoals(leanDir, f, goals, opts.Budget)
		return rs, f, text, err
	}
	rs, _, _, err := check()
	if err != nil {
		return nil, err
	}
	model, err := os.ReadFile(filepath.Join(ws.Root, report.LeanFile))
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(ws.Root, "agent", "prove", contract.PackageKey(report.Package))
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	for i := range goals {
		if rs[i].Proved {
			continue
		}
		orig := goals[i]
		var proved []string
		for j := 0; j < i; j++ {
			if rs[j].Proved {
				proved = append(proved, prover.TheoremName(goals[j].Func))
			}
		}
		feedback := ""
		for round := 1; round <= opts.Rounds; round++ {
			user := provePrompt(opts.Library, string(model), fids[i], goals[i].Func, goalStatement(goals[i], abis[i], fids[i]), proved, feedback)
			base := filepath.Join(logDir, fmt.Sprintf("%s-round-%d", goals[i].Func, round))
			os.WriteFile(base+".prompt.md", []byte(user), 0o644)
			start := time.Now()
			resp, err := llm.Complete(ctx, proveSystem, user)
			if err != nil {
				return nil, fmt.Errorf("%s round %d: %w", goals[i].Func, round, err)
			}
			os.WriteFile(base+".response.md", []byte(resp), 0o644)
			prelude, proof, perr := parseProof(resp)
			if perr == nil {
				perr = vetProof(prelude + "\n" + proof)
			}
			if perr != nil {
				fmt.Fprintf(out, "  %s round %d (%s): %v\n", goals[i].Func, round, time.Since(start).Round(time.Second), perr)
				feedback = "Your answer was not used: " + perr.Error()
				continue
			}
			goals[i].Prelude, goals[i].Proof = prelude, proof
			var f *prover.File
			var text string
			rs, f, text, err = check()
			if err != nil {
				return nil, err
			}
			os.WriteFile(base+".lean.log", []byte(text), 0o644)
			if rs[i].Proved {
				fmt.Fprintf(out, "  %s round %d (%s): proved\n", goals[i].Func, round, time.Since(start).Round(time.Second))
				break
			}
			fmt.Fprintf(out, "  %s round %d (%s): %s\n", goals[i].Func, round, time.Since(start).Round(time.Second), firstLine(rs[i].Error))
			feedback = "Lean rejected it. Your text in the file, with line numbers:\n\n" +
				numbered(leanDir, f.Rel, goals[i]) + "\nLean's errors:\n\n" + prover.Errors(text, f.Rel, 12000)
			if rs[i].Error != "" && !strings.Contains(feedback, "error") {
				feedback += "\n" + rs[i].Error
			}
		}
		if !rs[i].Proved {
			goals[i] = orig
			if rs, _, _, err = check(); err != nil {
				return nil, err
			}
		}
	}
	return rs, nil
}

// goalStatement is the theorem a prompt shows for goal g.
func goalStatement(g prover.Goal, abi *contract.ABI, fid int) string {
	st, err := prover.Statement(abi, fid, prover.TheoremName(g.Func), g.Post)
	if err != nil {
		return err.Error()
	}
	return st + " := by\n  <your proof>"
}

func provePrompt(library, model string, fid int, fn, statement string, contracts []string, feedback string) string {
	var b strings.Builder
	b.WriteString("# The proof library and worked examples\n\n")
	b.WriteString(library)
	b.WriteString("\n\n# The model of the function's package\n\n```lean\n")
	b.WriteString(model)
	fmt.Fprintf(&b, "```\n\n# Your goal\n\nThe function is %s, f%d in the model (lookup equation prog_f%d : prog.funcs[%d]? = some f%d, already stated). ", fn, fid, fid, fid, fid)
	b.WriteString("In the file, `prog`, `or0` (the memory oracle) and `h0` (the havoc values) are in scope, ")
	b.WriteString("with `open Go2Lean.SSA Go2Lean.GoSSA Go2Lean.GoSSA.WP` and the model's namespace. The theorem:\n\n```lean\n")
	b.WriteString(statement)
	b.WriteString("\n```\n")
	if len(contracts) > 0 {
		fmt.Fprintf(&b, "\nProved contracts you may use at calls (wpFun_mono, or wp_auto's using): %s.\n", strings.Join(contracts, ", "))
	}
	if feedback != "" {
		b.WriteString("\n# Your previous attempt\n\n")
		b.WriteString(feedback)
	}
	return b.String()
}

var proofFence = regexp.MustCompile("(?s)```lean (prelude|proof)[ \t]*\n(.*?)```")

// parseProof reads the two fenced blocks of an answer.
func parseProof(resp string) (prelude, proof string, err error) {
	for _, m := range proofFence.FindAllStringSubmatch(resp, -1) {
		switch m[1] {
		case "prelude":
			prelude = strings.TrimSpace(m[2])
		case "proof":
			proof = m[2]
		}
	}
	if strings.TrimSpace(proof) == "" {
		return "", "", fmt.Errorf("no ```lean proof block in the answer")
	}
	// The tactic block goes under ":= by": indent it if it is not.
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(proof, "\n "), "\n") {
		lines = append(lines, l)
	}
	indented := true
	for _, l := range lines {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, " ") {
			indented = false
		}
	}
	if !indented {
		for k, l := range lines {
			if strings.TrimSpace(l) != "" {
				lines[k] = "  " + l
			}
		}
	}
	return prelude, strings.Join(lines, "\n"), nil
}

// forbidden are the commands and terms an answer may not use: they add
// axioms or trust the compiler, change how the statement parses, or step
// out of the namespace the answer is written in.
var forbidden = regexp.MustCompile(`(?m)\b(sorry|admit|axiom|axioms|native_decide|ofReduceBool|implemented_by|extern|unsafe|notation|infix|infixl|infixr|prefix|postfix|macro|macro_rules|syntax|elab|elab_rules|declare_syntax_cat|run_cmd|run_tac|run_elab|initialize|builtin_initialize|export)\b|^\s*(namespace|section|end|open\s+.*\s+renaming|#exit)\b|_root_`)

func vetProof(text string) error {
	if m := forbidden.FindString(text); m != "" {
		return fmt.Errorf("the answer uses %q, which is not allowed", strings.TrimSpace(m))
	}
	return nil
}

// numbered is goal g's text in the file at rel (its prelude, statement and
// proof) with the file's line numbers, for reading Lean's errors against.
func numbered(leanDir, rel string, g prover.Goal) string {
	b, err := os.ReadFile(filepath.Join(leanDir, rel))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	name := "theorem " + prover.TheoremName(g.Func) + " "
	at := -1
	for k, l := range lines {
		if strings.HasPrefix(l, name) {
			at = k
			break
		}
	}
	if at < 0 {
		return ""
	}
	start := at
	if g.Prelude != "" {
		start = max(0, at-strings.Count(strings.TrimSpace(g.Prelude), "\n")-2)
	}
	end := at
	for end < len(lines)-1 && !strings.HasSuffix(lines[end], ":= by") {
		end++
	}
	end = min(len(lines)-1, end+strings.Count(g.Proof, "\n")+1)
	var out strings.Builder
	for k := start; k <= end; k++ {
		fmt.Fprintf(&out, "%4d  %s\n", k+1, lines[k])
	}
	return out.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
