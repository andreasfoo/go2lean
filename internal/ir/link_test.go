package ir

import "testing"

func TestLink(t *testing.T) {
	ret := func() Block { return Block{Term: Term{Kind: TermRet, V: 0}} }
	// main: f0 calls dep's f1 (cross-package) and reads havoc site 0.
	main := &Prog{Package: "m", HavocWidths: []int{64}, Funcs: []*Func{{
		Name: "F", NVals: 2, Blocks: []Block{
			{Instrs: []Instr{{Dst: 0, HavocSite: 0}}, Term: Term{Kind: TermXCall, CalleePkg: "d", Callee: 1, Dst: 1, Next: 1, Fallback: 1}},
			ret(),
		}}}}
	// dep: f0 (unused), f1 reads its havoc site 1 and names f0 as a value.
	dep := &Prog{Package: "d", Dependency: true, HavocWidths: []int{8, 32}, Funcs: []*Func{
		{Name: "G0", NVals: 1, Blocks: []Block{ret()}},
		{Name: "G1", NVals: 2, Blocks: []Block{{
			Instrs: []Instr{{Dst: 0, HavocSite: 1}, {Dst: 1, Op: FuncRef{ID: 0}}},
			Term:   Term{Kind: TermRet, V: 0}}}},
	}}
	other := &Prog{Package: "o", Dependency: true, Funcs: []*Func{{Name: "H", NVals: 1, Blocks: []Block{ret()}}}}
	got, err := Link(main, map[string]*Prog{"d": dep, "o": other}, func(p string) string { return "k_" + p })
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Funcs) != 3 { // o is not reached
		t.Fatalf("%d funcs, want 3", len(got.Funcs))
	}
	if tm := got.Funcs[0].Blocks[0].Term; tm.Kind != TermCall || tm.Callee != 2 || tm.CalleePkg != "" {
		t.Errorf("main's call: %+v", tm)
	}
	if len(got.HavocWidths) != 3 || got.HavocWidths[2] != 32 {
		t.Errorf("widths %v", got.HavocWidths)
	}
	g1 := got.Funcs[2].Blocks[0].Instrs
	if g1[0].HavocSite != 2 {
		t.Errorf("dep havoc site %d, want 2", g1[0].HavocSite)
	}
	if r, ok := g1[1].Op.(FuncRef); !ok || r.ID != 1 {
		t.Errorf("dep func ref %+v, want 1", g1[1].Op)
	}
	if main.Funcs[0].Blocks[0].Term.Kind != TermXCall || dep.Funcs[1].Blocks[0].Instrs[0].HavocSite != 1 {
		t.Error("Link changed its inputs")
	}
	// A JSON-encoding dependency cannot be linked (its tables are its own).
	dep.JSONTable = "{ descs := #[] }"
	if _, err := Link(main, map[string]*Prog{"d": dep}, func(p string) string { return p }); err == nil {
		t.Error("linked a JSON dependency")
	}
}
