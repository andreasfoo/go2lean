package poc

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed prepare.go.txt
var bundlePreparer []byte

type bundleFile struct {
	Path   string `json:"path"`   // module-relative destination
	Source string `json:"source"` // bundle-relative backing file
	SHA256 string `json:"sha256"`
}

type reproductionBundle struct {
	Version    int          `json:"version"`
	Package    string       `json:"package"`
	PackageDir string       `json:"package_dir"` // module-relative
	Test       string       `json:"test"`
	Race       bool         `json:"race"`
	Files      []bundleFile `json:"files"`
}

func portableRelative(path string, allowDot bool) bool {
	if path == "." {
		return allowDot
	}
	return path != "" && !strings.ContainsAny(path, "\\:\x00") &&
		!filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path &&
		path != ".." && !strings.HasPrefix(path, "../")
}

// writeArtifactFile atomically updates a regular artifact without following
// symlinks in its relative path. The workspace root itself is operator-owned.
func writeArtifactFile(base, relative string, data []byte) error {
	if !portableRelative(relative, false) {
		return fmt.Errorf("invalid artifact path %q", relative)
	}
	dir := base
	parents := strings.Split(filepath.ToSlash(filepath.Dir(relative)), "/")
	for _, component := range append([]string{"."}, parents...) {
		dir = filepath.Join(dir, component)
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			if err := os.Mkdir(dir, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact parent is not a regular directory: %s", dir)
		}
	}
	path := filepath.Join(base, filepath.FromSlash(relative))
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact is not a regular file: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".artifact-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// packageOverlayFiles is shared by actual replay and delivery so delivery
// cannot silently choose different contents. Replay currently applies only
// package-local overlays, not arbitrary dependency-package overlays.
func packageOverlayFiles(pkgDir string, overlay map[string][]byte) (map[string][]byte, error) {
	files := map[string][]byte{}
	for path, source := range overlay {
		if filepath.Dir(path) != pkgDir {
			continue
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, fmt.Errorf("ambiguous/non-absolute package overlay path: %s", path)
		}
		files[filepath.Base(path)] = source
	}
	return files, nil
}

// deliverBundle includes exactly the package-local source overlays used by
// replay, with confirmed schedule rewrites taking precedence just as they do
// in replayOne/raceOne. Target source/dependencies are not bundled.
func deliverBundle(pocDir, root, pkgDir, testName string, harness []byte, p *prepared, helpers map[string][]byte) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pkgRelative, err := filepath.Rel(root, pkgDir)
	if err != nil {
		return err
	}
	pkgRelative = filepath.ToSlash(pkgRelative)
	if !portableRelative(pkgRelative, true) {
		return fmt.Errorf("cannot deliver portable overlays for a package outside the selected module: %s", pkgDir)
	}
	b := reproductionBundle{
		Version: 1, Package: p.target.Package, PackageDir: pkgRelative,
		Test: "TestGo2LeanPoC_" + p.cx.ID, Race: p.race,
	}
	if p.cx.ID == "" || !token.IsIdentifier(b.Test) {
		return fmt.Errorf("invalid reproduction test identifier %q", b.Test)
	}
	seen := map[string]bool{}
	add := func(name, source string, data []byte) error {
		if !portableRelative(name, false) || filepath.Base(name) != name || !strings.HasSuffix(name, ".go") {
			return fmt.Errorf("portable replay requires package-local Go source files, got %q", name)
		}
		dest := filepath.ToSlash(filepath.Join(pkgRelative, name))
		if seen[dest] {
			return fmt.Errorf("duplicate reproduction destination %q", dest)
		}
		seen[dest] = true
		if err := writeArtifactFile(pocDir, source, data); err != nil {
			return err
		}
		b.Files = append(b.Files, bundleFile{
			Path: dest, Source: source, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
		})
		return nil
	}
	if err := add(testName, testName, harness); err != nil {
		return err
	}
	names := map[string]bool{}
	for name := range helpers {
		names[name] = true
	}
	for name := range p.replayed {
		names[name] = true
	}
	var sorted []string
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		data, rewritten := p.replayed[name]
		source := "replay/" + name
		if !rewritten {
			data, source = helpers[name], "support/"+name
		}
		if err := add(name, source, data); err != nil {
			return err
		}
	}
	manifest, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := writeArtifactFile(pocDir, "prepare.go", bundlePreparer); err != nil {
		return err
	}
	if err := writeArtifactFile(pocDir, "go.mod", []byte("// Isolate generated observations from enclosing module test sweeps.\nmodule go2lean.invalid/reproduction\n\ngo 1.26.0\n")); err != nil {
		return err
	}
	// Publish the manifest last, after all its sources have been written.
	return writeArtifactFile(pocDir, "bundle.json", append(manifest, '\n'))
}
