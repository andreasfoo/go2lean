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
		{GoFunc: "p.G", Params: []contract.ABIParam{{Go: "s", Kind: contract.ABIInt, Width: 64}}},
		{GoFunc: "p.G", Params: []contract.ABIParam{{Go: "m", Kind: contract.ABIMap}}},
		{GoFunc: "p.G", Method: "M"},
	} {
		if _, err := Statement(bad, 0, "G_spec", "True"); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
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
