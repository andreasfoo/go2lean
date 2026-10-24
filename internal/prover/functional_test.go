package prover

import (
	"strings"
	"testing"

	"github.com/andreasfoo/go2lean/internal/contract"
)

func TestStatement(t *testing.T) {
	abi := &contract.ABI{GoFunc: "p.F", Params: []contract.ABIParam{
		{Go: "n", Kind: contract.ABIInt, Width: 32, Model: []string{"n"}},
		{Go: "ok", Kind: contract.ABIBool, Model: []string{"ok"}},
		{Go: "xs", Kind: contract.ABISlice, ElemWidth: 64, Model: []string{"xs_ref", "xs_off", "xs_len", "xs_cap"}},
	}, Results: []contract.ABIResult{{GoType: "int"}}}
	st, err := Statement(abi, 7, "F_spec", "ret = .int 64 0")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"theorem F_spec (n : BitVec 32) (ok : Bool) (xs : List (BitVec 64)) (xs_len : BitVec 64) (xs_ref : Nat) (s : HState Val)",
		"(h_xs_len : xs_len.toInt = xs.length) (h_xs_ref : xs_ref ≠ 0) (h_xs : s.heap[xs_ref]? = some (.arr (xs.map (.int 64))))",
		"wpFun prog or0 h0 7 [.int 32 n, .bool ok, .slice xs_ref 0 xs_len xs_len]",
		"(fun ret s' => ret = .int 64 0) s",
	} {
		if !strings.Contains(st, want) {
			t.Errorf("statement lacks %q:\n%s", want, st)
		}
	}
	for _, bad := range []*contract.ABI{
		{GoFunc: "p.G", Params: []contract.ABIParam{{Go: "m", Kind: contract.ABIMap}}},
		{GoFunc: "p.G", Method: "M"},
	} {
		if _, err := Statement(bad, 0, "G_spec", "True"); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
	}
}

func TestLeanName(t *testing.T) {
	abi := &contract.ABI{GoFunc: "p.F", Params: []contract.ABIParam{{Go: "s", Kind: contract.ABIString}, {Go: "ret", Kind: contract.ABIInt, Width: 64}}}
	st, err := Statement(abi, 0, "F_spec", "True")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "(s_ : List (BitVec 8)) (s__len : BitVec 64) (s__ref : Nat) (ret_ : BitVec 64) (s : HState Val)") {
		t.Errorf("statement:\n%s", st)
	}
}

func TestAxiomsOf(t *testing.T) {
	out := "'X.A_spec' depends on axioms: [propext,\n sorryAx]\n'X.B_spec' does not depend on any axioms\n"
	if a := axiomsOf(out, "A_spec"); !strings.Contains(a, "sorryAx") {
		t.Errorf("A: got %q", a)
	}
	if a := axiomsOf(out, "B_spec"); a != "none" {
		t.Errorf("B: got %q", a)
	}
	if a := axiomsOf(out, "C_spec"); a != "" {
		t.Errorf("C: got %q", a)
	}
}

func TestSpecPost(t *testing.T) {
	abi := &contract.ABI{GoFunc: "p.Clamp", Params: []contract.ABIParam{
		{Go: "v", Kind: contract.ABIInt, Width: 64},
		{Go: "xs", Kind: contract.ABISlice, ElemWidth: 64},
	}, Results: []contract.ABIResult{{GoType: "int"}}}
	post, err := SpecPost(abi, "M", 9)
	if err != nil {
		t.Fatal(err)
	}
	want := "_root_.Go2Lean.GoSSA.WP.wpFun _root_.M.prog or0 h0 9 [.int 64 v, .slice xs_ref 0 xs_len xs_len, ret] (fun r _ => r = .bool true) s'"
	if post != want {
		t.Errorf("got  %s\nwant %s", post, want)
	}
	// No result: the spec takes the parameters only.
	abi.Results = nil
	if post, _ := SpecPost(abi, "M", 9); strings.Contains(post, "ret]") {
		t.Errorf("no result, yet ret passed: %s", post)
	}
	if n := TheoremName(Goal{Func: "F", Spec: "SpecF"}); n != "SpecF" {
		t.Errorf("name %s", n)
	}
	if n := TheoremName(Goal{Func: "F"}); n != "F_spec" {
		t.Errorf("name %s", n)
	}
}

func TestCompanionsAndStrings(t *testing.T) {
	abi := &contract.ABI{GoFunc: "p.F", Params: []contract.ABIParam{
		{Go: "xs", Kind: contract.ABISlice, ElemWidth: 64},
		{Go: "name", Kind: contract.ABIString},
		{Go: "n", Kind: contract.ABIInt, Width: 64},
	}, Results: []contract.ABIResult{{GoType: "int"}}}
	st, err := Statement(abi, 2, "F_spec", "True")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(name : List (BitVec 8)) (name_len : BitVec 64) (name_ref : Nat)",
		"(h_name : s.heap[name_ref]? = some (.arr (name.map (.int 8))))",
		"[.slice xs_ref 0 xs_len xs_len, .str name_ref 0 name_len, .int 64 n]",
	} {
		if !strings.Contains(st, want) {
			t.Errorf("statement lacks %q:\n%s", want, st)
		}
	}
	g := Goal{Func: "F", Post: "P", EmptyPost: "Q"}
	cs := Companions(g, abi)
	if len(cs) != 3 {
		t.Fatalf("%d companions, want 3 (xs, name, both)", len(cs))
	}
	names := []string{cs[0].Name, cs[1].Name, cs[2].Name}
	if strings.Join(names, ",") != "F_spec_empty_xs,F_spec_empty_name,F_spec_empty_xs_name" {
		t.Errorf("names %v", names)
	}
	if cs[2].Post != "Q" || len(cs[2].Empty) != 2 || Companions(cs[0], abi) != nil {
		t.Errorf("companion %+v", cs[2])
	}
	st, err = statement(abi, 2, "c", cs[2].Post, "prog", cs[2].Empty)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[.slice 0 0 0 0, .str 0 0 0, .int 64 n]",
		"let xs : List (BitVec 64) := []; let xs_len : BitVec 64 := 0; let xs_ref : Nat := 0; ",
		"let name : List (BitVec 8) := [];",
	} {
		if !strings.Contains(st, want) {
			t.Errorf("companion statement lacks %q:\n%s", want, st)
		}
	}
	if strings.Contains(st, "h_xs") || strings.Contains(st, "(xs :") {
		t.Errorf("a nil parameter is bound:\n%s", st)
	}
	if got := strings.Join(binderNames(abi, cs[2].Empty), " "); got != "n s" {
		t.Errorf("binders %q", got)
	}
	// One slice parameter: one companion, named _empty.
	one := &contract.ABI{GoFunc: "p.G", Params: []contract.ABIParam{{Go: "a", Kind: contract.ABISlice, ElemWidth: 64}}}
	if cs := Companions(Goal{Func: "G", Post: "P"}, one); len(cs) != 1 || cs[0].Name != "G_spec_empty" || cs[0].Post != "P" {
		t.Errorf("%+v", cs)
	}
}
