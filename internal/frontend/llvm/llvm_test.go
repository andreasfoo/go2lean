//go:build llvm15 || llvm16 || llvm17 || llvm18 || llvm19 || llvm20 || llvm21 || llvm22 || llvm23

package llvm

import (
	"os/exec"
	"strings"
	"testing"
)

func TestLowerOOB(t *testing.T) {
	for _, tool := range []string{"clang", "opt"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	bc, err := Compile("../../../examples/llvm/oob.c", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(bc)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Funcs) != 3 {
		t.Fatalf("got %d functions, want sum, avg, max_of", len(m.Funcs))
	}
	sum := m.Funcs[0]
	if sum.Name != "sum" || len(sum.Gens) != 2 || sum.Gens[0].K != "buf" || sum.Gens[0].EB != 8 ||
		sum.Gens[1].K != "len" || sum.Gens[1].Of != 0 {
		t.Errorf("sum: gens %+v, want an 8-byte buffer and its length", sum.Gens)
	}
	avg := m.Funcs[1]
	if avg.Name != "avg" || len(avg.Gens) != 2 || avg.Gens[0].K != "int" || avg.Gens[0].W != 32 {
		t.Errorf("avg: gens %+v, want two i32", avg.Gens)
	}
	for _, f := range m.Funcs {
		if len(f.Issues) != 0 {
			t.Errorf("%s: issues %v", f.Name, f.Issues)
		}
	}
	for _, want := range []string{"(.add true false true)", "(.load 64)", "(.gep 0 [8])", ".cond"} {
		if !strings.Contains(m.Lean, want) {
			t.Errorf("model lacks %s", want)
		}
	}
}
