package agent

import (
	"strings"
	"testing"
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
	} {
		if err := vetProof(bad); err == nil {
			t.Errorf("%q should be refused", bad)
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
