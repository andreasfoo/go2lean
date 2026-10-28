package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceOutsideCheckout(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module example.org/external\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf := workspaceFlag(fs)
	if err := fs.Parse([]string{"-module", module, "-workspace", "results"}); err != nil {
		t.Fatal(err)
	}
	w, env, err := openWorkspace(cf)
	if err != nil {
		t.Fatal(err)
	}
	if w.Root != filepath.Join(cwd, "results") {
		t.Fatalf("workspace = %s", w.Root)
	}
	if env.Module != module {
		t.Fatalf("module = %s", env.Module)
	}
}
