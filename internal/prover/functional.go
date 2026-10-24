package prover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/andreasfoo/go2lean/internal/contract"
)

// A Goal is a functional property of one function, for every input: every
// run that ends returns without panicking, in a state satisfying Post.
// Post is a Lean proposition over the parameters (by their Go names, a
// slice as a `List (BitVec w)` of its elements), `ret` (the result) and
// `s'` (the state the run ends in).
//
// The theorem go2lean states for it is the function's contract in
// `Go2Lean.Proof.WP` (`wpFun`), so a proved goal is also what a caller's
// proof applies at a call: goals are proved in order, each with the ones
// before it as contracts.
type Goal struct {
	Func string `json:"func"`
	// Name is the theorem's name; default the spec's name, or Func_spec.
	Name string `json:"name,omitempty"`
	Post string `json:"post,omitempty"`
	// Spec names a `go2lean:spec` function of Func to take as the
	// postcondition instead of Post: the spec, run in the model on the
	// arguments and the result from the state the run ends in, returns
	// true whenever it returns (SpecPost). The theorem is then about the
	// same property the search checks, with no translation by hand.
	Spec    string `json:"spec,omitempty"`
	SpecFid int    `json:"-"` // the spec's model id, set by the caller (SpecPost)
	// Prelude holds definitions and lemmas the proof uses (invariants),
	// written before the theorem; Proof is its tactic block (default:
	// wp_auto with the earlier goals' contracts).
	Prelude string `json:"prelude,omitempty"`
	Proof   string `json:"proof,omitempty"`
}

// GoalResult is what Lean made of a goal.
type GoalResult struct {
	Func    string `json:"func"`
	Theorem string `json:"theorem"`
	Proved  bool   `json:"proved"`
	Error   string `json:"error,omitempty"`
}

// Statement is a goal's theorem, stated from the ABI: the binders, the
// hypotheses that tie a slice parameter to the heap, and the conclusion.
// name is the theorem's name and fid the function's model id.
func Statement(abi *contract.ABI, fid int, name, post string) (string, error) {
	return statement(abi, fid, name, post, "prog")
}

// statement spells the program as prog: FunctionalFile names it in full,
// so that nothing a prelude defines can stand in for the model.
func statement(abi *contract.ABI, fid int, name, post, prog string) (string, error) {
	if abi.Method != "" || abi.Ctor != nil || abi.Variadic {
		return "", fmt.Errorf("%s: methods, constructor-built receivers and variadic functions are not supported", abi.GoFunc)
	}
	if len(abi.Results) > 1 {
		return "", fmt.Errorf("%s: more than one result is not supported", abi.GoFunc)
	}
	var binders, hyps, args []string
	for _, p := range abi.Params {
		x := p.Go
		if reserved[x] || x == "" || x == "_" {
			return "", fmt.Errorf("%s: parameter name %q cannot be a Lean binder here", abi.GoFunc, x)
		}
		switch {
		case p.Kind == contract.ABIBool:
			binders = append(binders, fmt.Sprintf("(%s : Bool)", x))
			args = append(args, fmt.Sprintf(".bool %s", x))
		case p.Kind == contract.ABIInt && p.Width > 0:
			binders = append(binders, fmt.Sprintf("(%s : BitVec %d)", x, p.Width))
			args = append(args, fmt.Sprintf(".int %d %s", p.Width, x))
		case p.Kind == contract.ABISlice && p.ElemParam == nil && p.ElemWidth > 0:
			// A slice of integers: its elements in the heap object at x_ref,
			// from offset 0, with length and capacity x_len.
			binders = append(binders, fmt.Sprintf("(%[1]s : List (BitVec %[2]d)) (%[1]s_len : BitVec 64) (%[1]s_ref : Nat)", x, p.ElemWidth))
			hyps = append(hyps,
				fmt.Sprintf("(h_%[1]s_len : %[1]s_len.toInt = %[1]s.length)", x),
				fmt.Sprintf("(h_%[1]s_ref : %[1]s_ref ≠ 0)", x),
				fmt.Sprintf("(h_%[1]s : s.heap[%[1]s_ref]? = some (.arr (%[1]s.map (.int %[2]d))))", x, p.ElemWidth))
			args = append(args, fmt.Sprintf(".slice %[1]s_ref 0 %[1]s_len %[1]s_len", x))
		default:
			return "", fmt.Errorf("%s: parameter %s (%s) is not supported (integers, bools and slices of integers are)", abi.GoFunc, x, p.GoType)
		}
	}
	binders = append(binders, "(s : HState Val)")
	if post == "" {
		return "", fmt.Errorf("%s: no postcondition", abi.GoFunc)
	}
	return fmt.Sprintf("theorem %s %s\n    %s :\n    _root_.Go2Lean.GoSSA.WP.wpFun %s or0 h0 %d [%s]\n      (fun ret s' => %s) s",
		name, strings.Join(binders, " "), strings.Join(hyps, " "), prog, fid, strings.Join(args, ", "), post), nil
}

// SpecPost is the postcondition a Go spec gives (Goal.Spec): the spec's
// run on the arguments, then the result (when the function has one), from
// the state the function's run ended in, returns true if it ends. module
// is the model's Lean module and sid the spec's model id.
func SpecPost(abi *contract.ABI, module string, sid int) (string, error) {
	st, err := statement(abi, 0, "_", "True", "prog")
	if err != nil {
		return "", err
	}
	// The argument list of the statement, then ret.
	i := strings.Index(st, " 0 [")
	j := strings.Index(st, "]\n")
	args := st[i+4 : j]
	if len(abi.Results) == 1 {
		if args != "" {
			args += ", "
		}
		args += "ret"
	}
	return fmt.Sprintf("_root_.Go2Lean.GoSSA.WP.wpFun _root_.%s.prog or0 h0 %d [%s] (fun r _ => r = .bool true) s'", module, sid, args), nil
}

// reserved are the names the statement binds itself and the Lean keywords
// a Go parameter may be called.
var reserved = map[string]bool{"s": true, "ret": true, "or0": true, "h0": true, "prog": true,
	"at": true, "by": true, "do": true, "end": true, "fun": true, "have": true, "in": true, "let": true,
	"match": true, "show": true, "then": true, "with": true, "from": true, "where": true, "open": true}

// binderNames are the statement's binders and hypotheses, in order.
func binderNames(abi *contract.ABI) []string {
	var names, hyps []string
	for _, p := range abi.Params {
		x := p.Go
		if p.Kind == contract.ABISlice {
			names = append(names, x, x+"_len", x+"_ref")
			hyps = append(hyps, "h_"+x+"_len", "h_"+x+"_ref", "h_"+x)
		} else {
			names = append(names, x)
		}
	}
	return append(append(names, "s"), hyps...)
}

// TheoremName is the name of a goal's theorem.
func TheoremName(g Goal) string {
	switch {
	case g.Name != "":
		return ident.ReplaceAllString(g.Name, "_")
	case g.Spec != "":
		return ident.ReplaceAllString(g.Spec, "_")
	}
	return ident.ReplaceAllString(g.Func, "_") + "_spec"
}

// File is a written FunctionalFile: its path relative to the Lean project,
// and for each goal the lines (1-based, inclusive) of its check.
type File struct {
	Rel    string
	Checks [][2]int
}

// FunctionalFile writes the theorems of the goals, in order, into one file
// of the workspace's Lean project and returns its path relative to leanDir.
// abis are the goals' functions' ABIs and fids their model ids, whose
// lookup equations the proofs use.
func FunctionalFile(leanDir string, report *contract.ModelReport, goals []Goal, abis []*contract.ABI, fids []int) (*File, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `import Go2Lean.Proof.Simp
import Go2Lean.Proof.Tactic
import %[1]s

/-! Generated by go2lean prove: functional theorems of package %[2]s. Each
is the function's contract (partial correctness: every run that ends returns
without panicking, in a state satisfying the postcondition). -/

open Go2Lean.SSA Go2Lean.GoSSA Go2Lean.GoSSA.WP
open %[1]s

set_option maxHeartbeats 4000000
set_option linter.unusedVariables false

namespace Go2Lean.Proofs.Functional.%[3]s

def or0 : Oracle := { mem := fun _ _ => 0#64 }
def h0 : Havoc Val := fun _ _ => .unit

`, report.LeanModule, report.Package, ident.ReplaceAllString(contract.PackageKey(report.Package), "_"))
	seen := map[int]bool{}
	lookups := append([]int{}, fids...)
	for _, g := range goals {
		if g.Spec != "" {
			lookups = append(lookups, g.SpecFid)
		}
	}
	for _, id := range lookups {
		if !seen[id] {
			seen[id] = true
			fmt.Fprintf(&b, "theorem prog_f%[1]d : prog.funcs[%[1]d]? = some f%[1]d := rfl\n", id)
		}
	}
	var before []string
	for i, g := range goals {
		proof := g.Proof
		if proof == "" {
			proof = AutoProof(fids[i], before)
			if g.Spec != "" {
				// What wp_auto leaves at each return is the spec's run.
				proof += fmt.Sprintf("\n  all_goals (apply wpFun_of_wpPt prog_f%[1]d; wp_auto prog_f%[1]d [f%[1]d])", g.SpecFid)
			}
		}
		st, err := statement(abis[i], fids[i], TheoremName(g), g.Post, "_root_."+report.LeanModule+".prog")
		if err != nil {
			return nil, err
		}
		if g.Prelude != "" {
			fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(g.Prelude))
		}
		fmt.Fprintf(&b, "\n%s := by\n%s\n", st, proof)
		before = append(before, TheoremName(g))
	}
	ns := "Go2Lean.Proofs.Functional." + ident.ReplaceAllString(contract.PackageKey(report.Package), "_")
	fmt.Fprintf(&b, "\nend %s\n", ns)
	// Each theorem again, read outside the namespace the preludes write
	// in: the types in the statement are the core's and the model's.
	b.WriteString("\nsection Check\nopen Go2Lean.SSA Go2Lean.GoSSA\n")
	f := &File{}
	for i, g := range goals {
		st, err := statement(abis[i], fids[i], "_", g.Post, report.LeanModule+".prog")
		if err != nil {
			return nil, err
		}
		st = strings.Replace(st, "theorem _ ", "example ", 1)
		st = strings.ReplaceAll(st, " or0 h0 ", fmt.Sprintf(" %[1]s.or0 %[1]s.h0 ", ns))
		ex := fmt.Sprintf("%s :=\n  @%s.%s %s\n", st, ns, TheoremName(g), strings.Join(binderNames(abis[i]), " "))
		b.WriteString("\n")
		start := strings.Count(b.String(), "\n") + 1
		b.WriteString(ex)
		f.Checks = append(f.Checks, [2]int{start, start + strings.Count(ex, "\n") - 1})
	}
	b.WriteString("\nend Check\n\n")
	for _, g := range goals {
		fmt.Fprintf(&b, "#print axioms %s.%s\n", ns, TheoremName(g))
	}
	f.Rel = filepath.Join("Proofs", "Functional_"+ident.ReplaceAllString(contract.PackageKey(report.Package), "_")+".lean")
	path := filepath.Join(leanDir, f.Rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return f, os.WriteFile(path, []byte(b.String()), 0o644)
}

// AutoProof is the proof of a loop-free function's goal: walk it with
// wp_auto, applying the contracts of the goals before it at its calls.
func AutoProof(fid int, contracts []string) string {
	using := ""
	if len(contracts) > 0 {
		using = " using " + strings.Join(contracts, ", ")
	}
	return fmt.Sprintf("  apply wpFun_of_wpPt prog_f%[1]d\n  wp_auto prog_f%[1]d [f%[1]d]%[2]s", fid, using)
}

// LoopProof stands for the proof of a function with loops until one is
// written: the theorem is stated, and a caller's proof may use it, but
// it and everything that uses it count as not proved.
func LoopProof(heads []int) string {
	return fmt.Sprintf("  sorry -- loops at blocks %v: each needs an invariant (wpPt_loop)", heads)
}

// CheckGoals runs Lean on a FunctionalFile and reads, per goal, whether its
// theorem was proved by the kernel alone: no sorry (a failed proof, or one
// that used a failed theorem) and no Lean.ofReduceBool.
func CheckGoals(leanDir string, f *File, goals []Goal, budget time.Duration) ([]GoalResult, string, error) {
	rel := f.Rel
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lake", "lean", rel)
	cmd.Dir = leanDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	text := string(out)
	if ctx.Err() != nil {
		return nil, text, fmt.Errorf("lean: timeout after %s", budget)
	}
	if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(text, "error") {
		return nil, text, fmt.Errorf("lean: %v: %s", err, firstError(text))
	}
	errLines := errorLines(text, rel)
	var rs []GoalResult
	for i, g := range goals {
		name := TheoremName(g)
		r := GoalResult{Func: g.Func, Theorem: name}
		ax := axiomsOf(text, name)
		checked := true
		for _, l := range errLines {
			if i < len(f.Checks) && l >= f.Checks[i][0] && l <= f.Checks[i][1] {
				checked = false
			}
		}
		switch {
		case !checked:
			r.Error = "the theorem does not have the stated type outside its namespace (a prelude redefined a name the statement uses)"
		case ax == "":
			r.Error = "not checked: " + firstError(text)
		case strings.Contains(ax, "sorryAx"):
			r.Error = "not proved: " + firstError(text)
		case strings.Contains(ax, "ofReduceBool"):
			r.Error = "proved, but trusting the compiler (Lean.ofReduceBool)"
		default:
			r.Proved = true
		}
		rs = append(rs, r)
	}
	return rs, text, nil
}

// Errors are Lean's error messages on the file at rel, each with the lines
// that follow it up to the next message, at most limit bytes in all.
func Errors(text, rel string, limit int) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(text, "\n") {
		head := strings.Contains(l, ".lean:") && (strings.Contains(l, ": error") || strings.Contains(l, ": warning") || strings.Contains(l, ": info"))
		if head {
			in = strings.Contains(l, ": error") && strings.Contains(l, filepath.Base(rel))
		}
		if strings.HasPrefix(l, "'") && strings.Contains(l, "axioms") {
			in = false
		}
		if in {
			if b.Len()+len(l) > limit {
				b.WriteString("[...]\n")
				break
			}
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// errorLines are the lines of the file at rel that Lean reported errors on.
func errorLines(text, rel string) []int {
	var out []int
	base := filepath.Base(rel)
	for _, l := range strings.Split(text, "\n") {
		i := strings.Index(l, base+":")
		if i < 0 || !strings.Contains(l, ": error") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(l[i+len(base)+1:], "%d:", &n); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// axiomsOf is what `#print axioms` printed for the theorem (the list may
// wrap over several lines): "none", the list, or "" when it did not run.
func axiomsOf(s, name string) string {
	i := strings.Index(s, "."+name+"' depends on axioms")
	if i < 0 {
		if strings.Contains(s, "."+name+"' does not depend on any axioms") {
			return "none"
		}
		return ""
	}
	rest := s[i:]
	if j := strings.Index(rest, "]"); j >= 0 {
		rest = rest[:j+1]
	}
	return strings.Join(strings.Fields(rest), " ")
}
