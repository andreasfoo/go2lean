package constraint

import (
	"testing"

	"github.com/andreasfoo/go2lean/internal/contract"
)

func TestGensAndTypeTable(t *testing.T) {
	abi := &contract.ABI{
		Params: []contract.ABIParam{
			{Go: "n", GoType: "int8", Width: 8, Signed: true, Model: []string{"n"}, Kind: contract.ABIInt},
			{Go: "xs", GoType: "[]uint", ElemWidth: 64, Model: []string{"xs_ref", "xs_off", "xs_len", "xs_cap"}, Kind: contract.ABISlice, Elem: "uint"},
			{Go: "s", GoType: "string", Model: []string{"s"}, Kind: contract.ABIString},
			{Go: "l", GoType: "*Node", Model: []string{"l"}, Kind: contract.ABIPtr,
				Pointee: &contract.ABIParam{Go: "l", GoType: "Node", Model: []string{"l"}, Kind: contract.ABIStruct, TypeRef: "Node"}},
		},
		Types: []contract.ABIType{{GoType: "Node", Fields: []contract.ABIParam{
			{Go: "Val", GoType: "int", Width: 64, Signed: true, Model: []string{"Val"}, Kind: contract.ABIInt},
			{Go: "Next", GoType: "*Node", Model: []string{"Next"}, Kind: contract.ABIPtr,
				Pointee: &contract.ABIParam{Go: "Next", GoType: "Node", Model: []string{"Next"}, Kind: contract.ABIStruct, TypeRef: "Node"}},
		}}},
	}
	g, err := gens(abi, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := `[(.int "n" 8 true), (.slice "xs" 64 false 5), (.str "s" 5), (.ptr "l" (.ref "l" 0))]`
	if g != want {
		t.Fatalf("gens: got %s\nwant %s", g, want)
	}
	tbl, err := typeTable(abi, 5)
	if err != nil {
		t.Fatal(err)
	}
	if tbl != `#[[(.int "Val" 64 true), (.ptr "Next" (.ref "Next" 0))]]` {
		t.Fatalf("type table: %s", tbl)
	}
}

func TestLeanString(t *testing.T) {
	if got := leanString("a\"b\\c\n"); got != `"a\"b\\c\n"` {
		t.Fatalf("got %s", got)
	}
}
