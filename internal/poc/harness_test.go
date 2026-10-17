package poc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andreasfoo/go2lean/internal/contract"
)

func intParam(name string) contract.ABIParam {
	return contract.ABIParam{Go: name, GoType: "int", Width: 64, Signed: true, Model: []string{name}, Kind: contract.ABIInt}
}

func TestLiteralStructAndPointer(t *testing.T) {
	node := contract.ABIType{GoType: "Node", Fields: []contract.ABIParam{
		intParam("Val"),
		{Go: "Next", GoType: "*Node", Model: []string{"Next"}, Kind: contract.ABIPtr,
			Pointee: &contract.ABIParam{Go: "Next", GoType: "Node", Model: []string{"Next"}, Kind: contract.ABIStruct, TypeRef: "Node"}},
	}}
	abi := &contract.ABI{Types: []contract.ABIType{node}}
	p := contract.ABIParam{Go: "n", GoType: "*Node", Model: []string{"n"}, Kind: contract.ABIPtr,
		Pointee: &contract.ABIParam{Go: "n", GoType: "Node", Model: []string{"n"}, Kind: contract.ABIStruct, TypeRef: "Node"}}
	var v any
	if err := json.Unmarshal([]byte(`["1", ["2", null]]`), &v); err != nil {
		t.Fatal(err)
	}
	got, err := literal(abi, p, v)
	if err != nil {
		t.Fatal(err)
	}
	want := "&Node{Val: int(1), Next: &Node{Val: int(2), Next: (*Node)(nil)}}"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestLiteralScalarPointerAndString(t *testing.T) {
	p := contract.ABIParam{Go: "p", GoType: "*int", Model: []string{"p"}, Kind: contract.ABIPtr, Pointee: ptr(intParam("p"))}
	got, err := literal(&contract.ABI{}, p, "5")
	if err != nil || got != "func() *int { v := int(5); return &v }()" {
		t.Fatalf("got %q, %v", got, err)
	}
	s := contract.ABIParam{Go: "s", GoType: "string", Model: []string{"s"}, Kind: contract.ABIString}
	got, err = literal(&contract.ABI{}, s, "a\"b")
	if err != nil || got != `string("a\"b")` {
		t.Fatalf("got %q, %v", got, err)
	}
}

func ptr(p contract.ABIParam) *contract.ABIParam { return &p }

func TestHarnessMethodCallAndImports(t *testing.T) {
	abi := &contract.ABI{Method: "Withdraw", Params: []contract.ABIParam{
		{Go: "a", GoType: "*Account", Model: []string{"a"}, Kind: contract.ABIPtr,
			Pointee: &contract.ABIParam{Go: "a", GoType: "Account", Model: []string{"a"}, Kind: contract.ABIStruct,
				Fields: []contract.ABIParam{intParam("Balance"), {Go: "D", GoType: "time.Duration", TypePkg: "time", Width: 64, Signed: true, Model: []string{"D"}, Kind: contract.ABIInt}}}},
		intParam("amt"),
	}, Results: []contract.ABIResult{{GoType: "bool"}}}
	cx := &contract.Counterexample{ID: "cx1", Inputs: map[string]string{"amt": "-3"},
		Objects: map[string]json.RawMessage{"a": json.RawMessage(`["10", "7"]`)}}
	src, err := Harness("alias", &contract.Target{Func: "Account.Withdraw"}, &contract.Property{Kind: contract.KindNoPanic}, cx, abi)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"time"`, "a := &Account{Balance: int(10), D: time.Duration(7)}", "ret = a.Withdraw(amt)"} {
		if !strings.Contains(src, want) {
			t.Fatalf("harness lacks %q:\n%s", want, src)
		}
	}
}

func TestParseResultsMapsByTestName(t *testing.T) {
	out := "=== RUN   TestGo2LeanPoC_a\nGO2LEAN_RESULT {\"panicked\":false,\"ret\":\"1\"}\n--- PASS: TestGo2LeanPoC_a (0.00s)\n=== RUN   TestGo2LeanPoC_b\nGO2LEAN_RESULT {\"panicked\":true,\"panic\":\"runtime error: index out of range [2] with length 2\"}\n"
	rs := parseResults(out)
	if len(rs) != 2 || rs["a"].Ret != "1" || !rs["b"].Panicked {
		t.Fatalf("unexpected %+v", rs)
	}
	if ClassifyPanic(rs["b"].Panic) != contract.PanicIndexOutOfRange {
		t.Fatal("panic not classified")
	}
}
