package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/prover"
)

func TestParseProof(t *testing.T) {
	resp := "Here it is.\n\n```lean prelude\ndef inv (n : Nat) : Prop := n = n\n```\n\nand\n\n```lean proof\napply foo\nexact bar\n```\n"
	prelude, proof, err := parseProof(resp)
	if err != nil {
		t.Fatal(err)
	}
	if prelude != "def inv (n : Nat) : Prop := n = n" {
		t.Errorf("prelude %q", prelude)
	}
	if proof != "  apply foo\n  exact bar" {
		t.Errorf("proof %q", proof)
	}
	// Already indented: kept as is.
	_, proof, _ = parseProof("```lean proof\n  intro x\n    simp\n```")
	if proof != "  intro x\n    simp" {
		t.Errorf("indented proof %q", proof)
	}
	if _, _, err := parseProof("```lean\nexact foo\n```"); err == nil {
		t.Error("an answer without a proof block should be refused")
	}
}

func TestVetProof(t *testing.T) {
	for _, bad := range []string{
		"exact sorry",
		"axiom cheat : False",
		"by native_decide",
		"local notation \"wpFun\" => True",
		"macro \"x\" : tactic => `(tactic| rfl)",
		"end Go2Lean.Proofs.Functional.p",
		"namespace Other",
		"theorem t : _root_.Foo := x",
		"@[simp] local notation \"x\" => 1",
		"open Foo renaming bar → baz",
		"  exact (sorry : False)",
	} {
		if err := vetProof(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	// Words in comments and names are not commands.
	for _, fine := range []string{
		"-- the sorted prefix grows\ntheorem sorted_prefix : True := trivial",
		"/- uses sorry nowhere: an axiom-free proof -/\nexact trivial",
		"theorem t : True := by\n  simp [List.isPrefixOf, prefix_eq]",
	} {
		if err := vetProof(fine); err != nil {
			t.Errorf("%q: %v", fine, err)
		}
	}
	ok := "def countInv (env : Env Val) : Prop := True\ntheorem l : True := trivial\n  apply wpPt_loop prog_f1 inv\n  · intro n env s hI ih\n    simp [List.isPrefixOf]"
	if err := vetProof(ok); err != nil {
		t.Errorf("%q: %v", ok, err)
	}
	if !strings.Contains(proveSystem, "```lean proof") {
		t.Error("the system prompt should show the answer format")
	}
}

func TestDeclared(t *testing.T) {
	goals := []prover.Goal{
		{Prelude: "def inv (x : Nat) : Prop := True\n@[simp] theorem inv_ok : True := trivial\nabbrev post := 1"},
		{Prelude: "def mine := 2"},
	}
	got := strings.Join(declared(goals, 1), ",")
	if got != "inv,inv_ok,post" {
		t.Errorf("got %s", got)
	}
}

func TestLoopPaths(t *testing.T) {
	// CountPositive: 0 → 1 (header); 1 → 2 (body) | 3 (done); 2 → 4 | 1; 4 → 1.
	f := contract.ModelFunc{ID: 1, Name: "CountPositive", LoopHeaders: []int{1},
		Succs: [][]int{{1}, {2, 3}, {4, 1}, nil, {1}}}
	got := LoopPaths(f)
	for _, want := range []string{
		"1 → 2 → 4 → 1  (back edge",
		"1 → 2 → 1  (back edge",
		"1 → 3  (returns or panics)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if LoopPaths(contract.ModelFunc{}) != "" {
		t.Error("a function without loops has no paths")
	}
}

func TestWithLeanTools(t *testing.T) {
	cfg := t.TempDir() + "/mcp.json"
	llm, err := WithLeanTools(&ClaudeCode{Effort: "high"}, "/bin/lean-lsp-mcp", "/ws/lean", cfg)
	if err != nil {
		t.Fatal(err)
	}
	cc := llm.(*ClaudeCode)
	if cc.MCPConfig != cfg || cc.Effort != "high" || cc.MaxTurns == 0 {
		t.Errorf("%+v", cc)
	}
	for _, tool := range cc.AllowedTools {
		if !strings.HasPrefix(tool, "mcp__lean__lean_") || strings.Contains(tool, "search") && tool != "mcp__lean__lean_local_search" {
			t.Errorf("tool %s", tool)
		}
	}
	b, err := os.ReadFile(cfg)
	if err != nil || !strings.Contains(string(b), "/ws/lean") || !strings.Contains(string(b), "lean_loogle") {
		t.Errorf("config %s (%v)", b, err)
	}
	// Other LLMs are left as they are.
	m := &Mock{Path: "x"}
	if got, _ := WithLeanTools(m, "b", "d", cfg); got != m {
		t.Error("a mock should not change")
	}
}
