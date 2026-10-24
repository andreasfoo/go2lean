// Package repo locates the go2lean checkout and its Lean project from any
// working directory inside it.
package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// LeanDir is the lake project of go2lean's core Lean library, as checked
// out. Runs build and use a snapshot of it (CoreDir).
func LeanDir(root string) string { return filepath.Join(root, "lean") }

// keepCores is how many core snapshots CoreDir keeps.
const keepCores = 4

// CoreDir is the core lake project a run builds and its workspaces require:
// a copy of the checkout's core sources under the user cache directory
// ($GO2LEAN_CACHE, else os.UserCacheDir()/go2lean), named by a hash of
// their contents. Runs of the same sources share its build; editing the
// checkout makes a new snapshot instead of changing the one a running
// search links against. The most recently used keepCores are kept.
func CoreDir(root string) (string, error) {
	src := LeanDir(root)
	files, err := CoreFiles(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", f, len(b))
		h.Write(b)
	}
	return coreSnapshot(src, files, h.Sum(nil))
}

// CoreFiles are the core library's files in the checkout (relative to
// LeanDir): its sources, lakefile, toolchain and manifest, without the
// generated models, the search engines' sources or the hand proofs.
func CoreFiles(root string) ([]string, error) {
	src := LeanDir(root)
	var files []string
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			switch rel {
			case ".lake", "Search", "Proofs", filepath.Join("Go2Lean", "Generated"):
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".lean") || rel == "lakefile.toml" || rel == "lean-toolchain" || rel == "lake-manifest.json" {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func coreSnapshot(src string, files []string, sum []byte) (string, error) {
	base := os.Getenv("GO2LEAN_CACHE")
	if base == "" {
		c, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(c, "go2lean")
	}
	dir := filepath.Join(base, "core-"+hex.EncodeToString(sum)[:16])
	if _, err := os.Stat(filepath.Join(dir, ".complete")); err != nil {
		tmp := fmt.Sprintf("%s.tmp-%d", dir, os.Getpid())
		os.RemoveAll(tmp)
		for _, f := range files {
			b, err := os.ReadFile(filepath.Join(src, f))
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(tmp, f)), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(tmp, f), b, 0o644); err != nil {
				return "", err
			}
		}
		if err := os.WriteFile(filepath.Join(tmp, ".complete"), nil, 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, dir); err != nil {
			os.RemoveAll(tmp) // another run made it first
			if _, err := os.Stat(filepath.Join(dir, ".complete")); err != nil {
				return "", fmt.Errorf("core snapshot %s: %w", dir, err)
			}
		}
	}
	now := time.Now()
	os.Chtimes(dir, now, now)
	pruneCores(base, dir)
	return dir, nil
}

// pruneCores removes all but the keepCores most recently used snapshots.
func pruneCores(base, keep string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	type snap struct {
		path string
		used time.Time
	}
	var snaps []snap
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "core-") || strings.Contains(e.Name(), ".tmp-") {
			continue
		}
		if info, err := e.Info(); err == nil {
			snaps = append(snaps, snap{filepath.Join(base, e.Name()), info.ModTime()})
		}
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].used.After(snaps[j].used) })
	for i, sn := range snaps {
		if i >= keepCores && sn.path != keep {
			os.RemoveAll(sn.path)
		}
	}
}

// WorkspaceLeanDir is a workspace's own lake project: its generated models
// and its search driver (see ssaout.EnsureProject).
func WorkspaceLeanDir(ws string) string { return filepath.Join(ws, "lean") }
