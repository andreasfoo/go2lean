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

func TestExtractGoPrefersSpecBlock(t *testing.T) {
	resp := "fix:\n```go\nfunc Last(xs []int) int { return xs[len(xs)-1] }\n```\nspecs:\n```go\npackage p\n\n// go2lean:spec Last\nfunc SpecLast(xs []int, ret int) bool { return true }\n```\n"
	got := extractGo(resp)
	if !strings.Contains(got, "go2lean:spec") || strings.Contains(got, "func Last(") {
		t.Fatalf("picked the wrong block: %q", got)
	}
	if !hasSpecBlock(resp) || hasSpecBlock("```go\nfunc F() {}\n```") {
		t.Fatal("hasSpecBlock misclassified")
	}
}

func TestExtractGoIndentedFence(t *testing.T) {
	resp := "- fix:\n  ```go\n  func Last(xs []int) int { return xs[len(xs)-1] }\n  ```\n\n```go\npackage p\n\n// go2lean:spec Last\nfunc SpecLast(xs []int, ret int) bool { return true }\n```\n"
	got := extractGo(resp)
	if !strings.HasPrefix(got, "package p") || strings.Contains(got, "func Last(") {
		t.Fatalf("indented fence swallowed the spec block: %q", got)
	}
}

func TestNormalizeSpec(t *testing.T) {
	got := normalizeSpec("// header\npackage yourpkg\n\nfunc F() {}\n", "oob")
	if !strings.Contains(got, "package oob\n") || strings.Contains(got, "yourpkg") {
		t.Fatalf("package clause not normalised: %q", got)
	}
	if got := normalizeSpec("func F() {}\n", "oob"); !strings.HasPrefix(got, "package oob\n") {
		t.Fatalf("missing clause not added: %q", got)
	}
}
