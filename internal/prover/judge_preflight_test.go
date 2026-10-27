package prover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJudgeReportsSandboxDependencyBeforeBuild(t *testing.T) {
	t.Setenv("GO2LEAN_LEAN_SANDBOX", "")
	t.Setenv("COMPARATOR_BWRAP", filepath.Join(t.TempDir(), "missing-bwrap"))
	dir := filepath.Join(t.TempDir(), "judge-must-not-be-created")
	_, err := Judge(JudgeInput{Dir: dir, Candidates: []int{0}})
	if err == nil || !strings.Contains(err.Error(), "comparator sandbox") {
		t.Fatalf("dependency not diagnosed: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("judge created project before dependency check: %v", err)
	}
}
