// Package repo locates the go2lean checkout and its Lean project from any
// working directory inside it.
package repo

import (
	"errors"
	"os"
	"path/filepath"
)

// Root walks up from the working directory to the directory holding go.mod.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// LeanDir is the lake project of go2lean's core Lean library.
func LeanDir(root string) string { return filepath.Join(root, "lean") }

// WorkspaceLeanDir is a workspace's own lake project: its generated models
// and its search driver (see ssaout.EnsureProject).
func WorkspaceLeanDir(ws string) string { return filepath.Join(ws, "lean") }
