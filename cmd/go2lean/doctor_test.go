package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorMissingTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	if err := diagnose(&out); err == nil {
		t.Fatal("missing tools accepted")
	}
	for _, want := range []string{"FAIL", "go", "elan", "lake", "prlimit", "elan toolchain install leanprover/lean4:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
}

func TestDoctorNoTargetOrToolchainExecution(t *testing.T) {
	bin := t.TempDir()
	// A marker would be created if doctor invoked a build/version check.
	marker := filepath.Join(t.TempDir(), "executed")
	for _, name := range []string{"go", "lake", "prlimit"} {
		src := "#!/bin/sh\necho invoked > '" + marker + "'\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(src), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	elan := "#!/bin/sh\n[ \"$1 $2\" = 'toolchain list' ] || exit 1\necho leanprover/lean4:v0.0.0\n"
	if err := os.WriteFile(filepath.Join(bin, "elan"), []byte(elan), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var out bytes.Buffer
	if err := diagnose(&out); err == nil {
		t.Fatal("wrong toolchain accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("doctor ran tool: %v", err)
	}
}
