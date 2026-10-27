package builtin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SourceGraph is a content fingerprint, not a copy of target code or a
// credential-bearing environment dump. Logical package/file keys allow an
// identical clone/cache to be checked at a different filesystem location.
type SourceGraph struct {
	Package    string            `json:"package"`
	PackageDir string            `json:"package_dir"`
	GoVersion  string            `json:"go_version"`
	GOOS       string            `json:"goos"`
	GOARCH     string            `json:"goarch"`
	ConfigHash string            `json:"config_sha256"`
	Files      map[string]string `json:"files"` // "" denotes an absent optional file
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular source file: %s", path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func canonicalDigest(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

type probeBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *probeBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > b.limit-b.Len() {
		p = p[:b.limit-b.Len()]
		b.exceeded = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func localRelative(path string) bool {
	return path == "." || (path != "" && !filepath.IsAbs(path) &&
		!strings.ContainsAny(path, "\\:\x00") && path != ".." &&
		!strings.HasPrefix(path, "../") && filepath.ToSlash(filepath.Clean(path)) == path)
}

func sourceProbe(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.WaitDelay = 2 * time.Second
	// Do not turn fingerprinting into dependency/toolchain installation.
	blocked := map[string]bool{"GOPROXY": true, "GOSUMDB": true, "GOVCS": true, "GOTELEMETRY": true}
	for _, entry := range os.Environ() {
		if !blocked[strings.SplitN(entry, "=", 2)[0]] {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOPROXY=off", "GOSUMDB=off", "GOVCS=*:off", "GOTELEMETRY=off")
	stdout := probeBuffer{limit: 64 << 20}
	stderr := probeBuffer{limit: 1 << 20}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("builtin source fingerprint: go %s: %w\n%s", strings.Join(args, " "), err, &stderr)
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("builtin source fingerprint: metadata output exceeded safety limit")
	}
	return stdout.Bytes(), nil
}

// CaptureSources fingerprints the selected package's source/test dependency
// graph, module manifests, selected embed/native files and build configuration.
// Initial support is the default-build, single-module mode: active workspaces
// and custom GOFLAGS are refused, not silently cleared.
//
// Like the compile stage, this uses trusted Go tooling. It does not execute
// target functions. Tool executables/system libraries and filesystem races
// are not authenticated by these source hashes.
func CaptureSources(root, packageDir string) (*SourceGraph, error) {
	if !localRelative(packageDir) {
		return nil, fmt.Errorf("builtin: package must be inside the selected module")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	keys := strings.Fields("GOVERSION GOOS GOARCH CGO_ENABLED GOAMD64 GOARM GOARM64 GOMIPS GOMIPS64 GOPPC64 GORISCV64 GOWASM GOEXPERIMENT CC CXX CGO_CFLAGS CGO_CPPFLAGS CGO_CXXFLAGS CGO_FFLAGS CGO_LDFLAGS GOFLAGS GOWORK GOFIPS140")
	configJSON, err := sourceProbe(ctx, root, append([]string{"env", "-json"}, keys...)...)
	if err != nil {
		return nil, err
	}
	var config map[string]string
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return nil, err
	}
	if config["GOFLAGS"] != "" || (config["GOWORK"] != "" && config["GOWORK"] != "off") {
		return nil, fmt.Errorf("builtin snapshots currently require empty GOFLAGS and no active go.work; use a default-build scratch module (GOWORK=off)")
	}
	if !version.IsValid(config["GOVERSION"]) || version.Compare(config["GOVERSION"], "go1.26") < 0 ||
		config["GOOS"] != runtime.GOOS || config["GOARCH"] != runtime.GOARCH || strconv.IntSize != 64 {
		return nil, fmt.Errorf("builtin snapshots require Go 1.26+ on the native 64-bit platform (no cross compilation)")
	}
	config["GOWORK"] = "off"
	config["GODEBUG"] = os.Getenv("GODEBUG")
	configHash, err := canonicalDigest(config)
	if err != nil {
		return nil, err
	}
	fields := "ImportPath,Name,Dir,ForTest,Error,DepsErrors,Incomplete,Module,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SwigFiles,SwigCXXFiles,SysoFiles,EmbedFiles,TestGoFiles,XTestGoFiles,TestEmbedFiles,XTestEmbedFiles"
	data, err := sourceProbe(ctx, root, "list", "-mod=readonly", "-e", "-deps", "-test", "-json="+fields, "--", "./"+packageDir)
	if err != nil {
		return nil, err
	}
	type module struct {
		Path, Version, GoMod, Dir string
		Main                      bool
		Replace                   *module
	}
	type pkg struct {
		ImportPath, Name, Dir, ForTest                                                                          string
		Error                                                                                                   *struct{ Err string }
		DepsErrors                                                                                              []struct{ Err string }
		Incomplete                                                                                              bool
		Module                                                                                                  *module
		GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles []string
		EmbedFiles, TestGoFiles, XTestGoFiles, TestEmbedFiles, XTestEmbedFiles                                  []string
	}
	var packages []pkg
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var p pkg
		err := decoder.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil || p.ImportPath == "" {
			return nil, fmt.Errorf("builtin: malformed Go package graph")
		}
		if p.Error != nil || len(p.DepsErrors) != 0 || p.Incomplete {
			return nil, fmt.Errorf("builtin: incomplete cached source/test graph at %s; prepare dependencies with trusted tooling and rerun doctor -deps", p.ImportPath)
		}
		packages = append(packages, p)
	}
	g := &SourceGraph{PackageDir: packageDir, GoVersion: config["GOVERSION"],
		GOOS: config["GOOS"], GOARCH: config["GOARCH"], ConfigHash: configHash, Files: map[string]string{}}
	selected, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(packageDir)))
	if err != nil {
		return nil, err
	}
	contained, err := filepath.Rel(root, selected)
	if err != nil || !localRelative(filepath.ToSlash(contained)) {
		return nil, fmt.Errorf("builtin: package directory resolves outside the scratch module")
	}
	for _, p := range packages {
		dir, err := filepath.EvalSymlinks(p.Dir)
		if err == nil && dir == selected && p.ForTest == "" && p.Module != nil && p.Module.Main {
			// The synthetic .test main has a cache file rather than source
			// in this directory. It is regenerated, not a source dependency.
			if p.Name == "main" && len(p.GoFiles) == 1 && filepath.IsAbs(p.GoFiles[0]) {
				continue
			}
			moduleRoot, err := filepath.EvalSymlinks(p.Module.Dir)
			if err != nil || moduleRoot != root {
				return nil, fmt.Errorf("builtin: -module must select the actual main module root")
			}
			g.Package = p.ImportPath
		}
	}
	if g.Package == "" {
		return nil, fmt.Errorf("builtin: selected package is not in the main scratch module")
	}
	add := func(key, path string, optional bool) error {
		sum, err := fileDigest(path)
		if optional && os.IsNotExist(err) {
			sum, err = "", nil
		}
		if err != nil {
			return err
		}
		if old, exists := g.Files[key]; exists && old != sum {
			return fmt.Errorf("builtin: ambiguous source identity %s", key)
		}
		g.Files[key] = sum
		return nil
	}
	for _, p := range packages {
		if p.ImportPath == g.Package+".test" && p.Name == "main" && p.ForTest == "" {
			continue
		}
		var names []string
		for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles,
			p.SwigFiles, p.SwigCXXFiles, p.SysoFiles, p.EmbedFiles, p.TestGoFiles, p.XTestGoFiles, p.TestEmbedFiles, p.XTestEmbedFiles} {
			names = append(names, group...)
		}
		sort.Strings(names)
		for _, name := range names {
			if !localRelative(filepath.ToSlash(name)) || name == "." {
				return nil, fmt.Errorf("builtin: unsupported generated/absolute source path in %s", p.ImportPath)
			}
			if err := add("package:"+p.ImportPath+"/"+filepath.ToSlash(name), filepath.Join(p.Dir, name), false); err != nil {
				return nil, err
			}
		}
		if m := p.Module; m != nil {
			identity := m.Path + "@" + m.Version
			actual := m
			if m.Replace != nil {
				actual = m.Replace
			}
			if actual.GoMod != "" {
				if err := add("module:"+identity+"/go.mod", actual.GoMod, false); err != nil {
					return nil, err
				}
				if m.Main {
					if err := add("module:"+identity+"/go.sum", filepath.Join(filepath.Dir(actual.GoMod), "go.sum"), true); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return g, nil
}

func (g *SourceGraph) Equal(other *SourceGraph) bool {
	a, errA := canonicalDigest(g)
	b, errB := canonicalDigest(other)
	return errA == nil && errB == nil && a == b
}
