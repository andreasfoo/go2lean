package poc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportedFunc(t *testing.T) {
	for f, want := range map[string]bool{
		"Join": true, "join": false, "Counter.Share": true, "counter.Share": false,
		"Counter.share": false, "Last$1": false,
	} {
		if got := exportedFunc(baseFunc(f)); got != want {
			t.Errorf("exportedFunc(%q) = %v", f, got)
		}
	}
	if got := baseFunc("Counter.Share~NewCounter"); got != "Counter.Share" {
		t.Errorf("baseFunc = %q", got)
	}
	if got := baseFunc("ContainsFunc[[]int, int]"); got != "ContainsFunc" {
		t.Errorf("baseFunc = %q", got)
	}
}

func TestNilSites(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(`package m

type T struct{ p *int }

func (t *T) M(p *int, q *int) int { return *p + *q }

func F(p *int) int { return *p }
func G(p *int) int { return *p }
func H(p *int) int { return *p }
func K(p *int) int { return *p }

func find(ok bool) *int {
	if ok {
		x := 1
		return &x
	}
	return nil
}

func relay(p *int) int { return G(p) }

func L(p *int) int { return *p }

func trunc(s string, n int) string { return s[:n] }

func useTrunc() { _ = trunc("abc", 2); _ = trunc("abcd", 3) }

type E struct{ msg string }

func (e *E) Error() string { return e.msg }

func check(bad bool) error {
	var e *E
	if bad {
		e = &E{"bad"}
	}
	return e // a typed nil when !bad
}

func guardedUse(ok bool) {
	if p := find(ok); p != nil {
		L(p)
	}
}

func use(t T, ok bool) {
	x := 1
	F(&x)
	F(nil)
	relay(nil)
	H(find(ok))
	K(t.p)
	K(&x)
	var s T
	s.M(&x, &x)
}
`), 0o644)
	c := newCallerIndex(dir)
	for _, tc := range []struct {
		fn                string
		yes, maybe, calls int
	}{
		{"F", 1, 0, 2},       // a nil literal at one of two sites
		{"G", 1, 0, 1},       // nil through relay's parameter
		{"H", 1, 0, 1},       // nil returned by find
		{"K", 0, 1, 2},       // a field: unknown; &x: never nil
		{"T.M", 0, 0, 1},     // &x only
		{"L", 0, 0, 1},       // find may return nil, but p != nil guards the call
		{"E.Error", 1, 0, 0}, // no static call; a nil *E is boxed in check
	} {
		names := []string{"p"}
		if tc.fn == "T.M" {
			names = []string{"q"}
		}
		if tc.fn == "E.Error" {
			names = []string{"e"}
		}
		yes, maybe, calls, ok := c.nilSites("m", tc.fn, names)
		if !ok || len(yes) != tc.yes || len(maybe) != tc.maybe || calls != tc.calls {
			t.Errorf("%s: yes %v maybe %v calls %d ok %v", tc.fn, yes, maybe, calls, ok)
		}
	}
}

func TestConstArgs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(`package m

func trunc(s string, n int) string { return s[:n] }

func use(k int) {
	_ = trunc("abc", 2)
	_ = trunc("abcd", 3)
}

func clip(n int) int { return 10 / n }

func use2(k int) { _ = clip(k); _ = clip(4) }
`), 0o644)
	c := newCallerIndex(dir)
	vals, all, calls := c.constArgs("m", "trunc", []string{"n"})
	if !all || calls != 2 || len(vals["n"]) != 2 {
		t.Errorf("trunc: %v %v %d", vals, all, calls)
	}
	if _, all, _ := c.constArgs("m", "clip", []string{"n"}); all {
		t.Error("clip: one caller passes a variable")
	}
}
