package agent

import (
	"strings"
	"testing"
)

func TestExtractGo(t *testing.T) {
	resp := "some words\n```go\npackage p\n\nfunc F() {}\n```\ntrailing"
	got := extractGo(resp)
	if !strings.HasPrefix(got, "package p") || !strings.HasSuffix(got, "func F() {}\n") {
		t.Fatalf("unexpected block %q", got)
	}
	if extractGo("no code here") != "" {
		t.Fatal("expected empty result without a block")
	}
	if !strings.Contains(stripGo(resp), "(revised spec file") || strings.Contains(stripGo(resp), "package p") {
		t.Fatalf("stripGo left code in %q", stripGo(resp))
	}
}
