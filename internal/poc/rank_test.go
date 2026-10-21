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

type T struct{}

func (t *T) M(p *int, q *int) int { return *p + *q }

func F(p *int) int { return *p }

func use() {
	x := 1
	F(&x)
	F(nil)
	var t T
	t.M(&x, &x)
}
`), 0o644)
	c := newCallerIndex(dir)
	sites, calls, ok := c.nilSites("m", "F", []string{"p"})
	if !ok || calls != 2 || len(sites) != 1 {
		t.Errorf("F: sites %v calls %d ok %v", sites, calls, ok)
	}
	sites, calls, ok = c.nilSites("m", "T.M", []string{"q"})
	if !ok || calls != 1 || len(sites) != 0 {
		t.Errorf("T.M: sites %v calls %d ok %v", sites, calls, ok)
	}
}
