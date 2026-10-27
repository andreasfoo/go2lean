package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComparatorRejectsOldBwrap(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "old-bwrap")
	// Simulate a machine where basic isolation works but comparator cannot.
	src := `#!/bin/sh
for arg; do
 if [ "$arg" = --clearenv ]; then echo 'bwrap: Unknown option --clearenv' >&2; exit 1; fi
done
exit 0
`
	if err := os.WriteFile(bin, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPARATOR_BWRAP", bin)
	err := CheckComparator()
	if err == nil || !strings.Contains(err.Error(), "--clearenv") {
		t.Fatalf("old sandbox accepted: %v", err)
	}
}

func TestComparatorMissingOverride(t *testing.T) {
	t.Setenv("COMPARATOR_BWRAP", filepath.Join(t.TempDir(), "missing"))
	if err := CheckComparator(); err == nil {
		t.Fatal("missing override accepted")
	}
}
