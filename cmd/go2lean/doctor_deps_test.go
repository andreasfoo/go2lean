package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyReport(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		failed     bool
		want       string
	}{
		{"available", `{"ImportPath":"example.com/m"}`, false, "1 locally available"},
		{"missing", `{"ImportPath":"example.com/m","Error":{"Err":"missing module"}}`, true, "missing module"},
		{"missing package context", `{"ImportPath":"example.invalid/pkg","Error":{"Err":"lookup disabled"}}`, true, "example.invalid/pkg: lookup disabled"},
		{"transitive", `{"ImportPath":"example.com/m","DepsErrors":[{"Err":"missing transitive"}]}`, true, "missing transitive"},
		{"import stack", `{"ImportPath":"example.com/m","Error":{"Err":"lookup disabled","ImportStack":["example.com/m","example.invalid/pkg"]}}`, true, "example.com/m -> example.invalid/pkg"},
		{"incomplete", `{"ImportPath":"example.com/m","Incomplete":true}`, true, "incomplete package"},
		{"empty", "", true, "no packages matched"},
		{"malformed", "{", true, "malformed"},
		{"no path", `{}`, true, "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := reportDeps(&out, strings.NewReader(tc.data), "", nil)
			if (err != nil) != tc.failed || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("err=%v, output=%s", err, &out)
			}
		})
	}
	var out bytes.Buffer
	if err := reportDeps(&out, strings.NewReader(`{"ImportPath":"m"}`), "offline", fmt.Errorf("exit 1")); err == nil || !strings.Contains(out.String(), "offline") {
		t.Fatalf("command error ignored: %v %s", err, &out)
	}
	if err := reportDeps(&out, strings.NewReader(`{"ImportPath":"m"}`), `go: warning: "./missing/..." matched no packages`, nil); err == nil {
		t.Fatal("partially empty selection accepted")
	}
}

func TestDependencyProbeOverridesDownloadAndBuildSettings(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
test "$GOTOOLCHAIN" = local && test "$GOPROXY" = off &&
test "$GOSUMDB" = off && test "$GOVCS" = '*:off' &&
test "$GOFLAGS" = '' && test "$GOENV" = off && test "$GOTELEMETRY" = off &&
test "$GOWORK" = off || exit 2
test "$*" = 'list -mod=readonly -e -deps -test -json=ImportPath,Error,DepsErrors,Incomplete -- ./...' || exit 3
echo '{"ImportPath":"example.com/m"}'
`
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GOWORK", "off")
	for _, key := range []string{"GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GOVCS", "GOFLAGS", "GOENV", "GOTELEMETRY"} {
		t.Setenv(key, "must-be-overridden")
	}
	var out bytes.Buffer
	if err := diagnoseDeps(&out, []string{"./..."}); err != nil {
		t.Fatalf("%v: %s", err, &out)
	}
	for _, pattern := range []string{"-toolexec=unexpected", "example.com/m@latest", ""} {
		if err := diagnoseDeps(&out, []string{pattern}); err == nil {
			t.Errorf("accepted unsafe pattern %q", pattern)
		}
	}
}

func TestDependencyProbeRealOfflineModule(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOMODCACHE", t.TempDir())
	mod := []byte("module example.com/doctorfixture\n\ngo 1.26.0\n")
	if err := os.WriteFile("go.mod", mod, 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "target-executed")
	code := fmt.Sprintf("package fixture\nimport \"os\"\nfunc init() { os.WriteFile(%q, nil, 0600) }\n", marker)
	if err := os.WriteFile("fixture.go", []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := diagnoseDeps(&out, []string{"./..."}); err != nil {
		t.Fatalf("available module: %v: %s", err, &out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("target init executed: %v", err)
	}
	// Loading cgo metadata must not invoke a package's build tooling either.
	tool := filepath.Join(dir, "pkg-config")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho invoked > '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("PKG_CONFIG", tool)
	if err := os.WriteFile("cgo.go", []byte("package fixture\n// #cgo pkg-config: go2lean-sentinel\nimport \"C\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := diagnoseDeps(&out, []string{"./..."}); err != nil {
		t.Fatalf("cgo metadata: %v: %s", err, &out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("target build tooling executed: %v", err)
	}
	// A dependency used only by package tests still matters to replay.
	if err := os.WriteFile("fixture_test.go", []byte("package fixture\nimport _ \"example.invalid/notcached\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := diagnoseDeps(&out, []string{"./..."}); err == nil || !strings.Contains(out.String(), "example.invalid/notcached") {
		t.Fatalf("missing test dependency accepted: %v: %s", err, &out)
	}
	got, err := os.ReadFile("go.mod")
	if err != nil || !bytes.Equal(got, mod) {
		t.Fatalf("go.mod changed: %v: %s", err, got)
	}
	if _, err := os.Stat("go.sum"); !os.IsNotExist(err) {
		t.Fatalf("go.sum written: %v", err)
	}
	out.Reset()
	if err := diagnoseDeps(&out, []string{"./nothing/..."}); err == nil {
		t.Fatal("empty package match accepted")
	}
}

func TestDependencyProbeWorkspacePreservesResolutionAndFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.work":            "go 1.26.0\nuse (\n./app\n./lib\n)\nreplace example.com/replaced => ./replacement\n",
		"go.work.sum":        "",
		"app/go.mod":         "module example.com/app\n\ngo 1.26.0\nrequire example.com/replaced v0.0.0\n",
		"app/app.go":         "package app\nimport _ \"example.com/lib\"\nimport _ \"example.com/replaced\"\n",
		"lib/go.mod":         "module example.com/lib\n\ngo 1.26.0\n",
		"lib/lib.go":         "package lib\n",
		"replacement/go.mod": "module example.com/replaced\n\ngo 1.26.0\n",
		"replacement/lib.go": "package replaced\n",
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(root, "app"))
	t.Setenv("GOWORK", "")
	t.Setenv("GOMODCACHE", t.TempDir())
	var out bytes.Buffer
	if err := diagnoseDeps(&out, []string{"./..."}); err != nil {
		t.Fatalf("workspace resolution: %v: %s", err, &out)
	}
	for name, data := range files {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != data {
			t.Errorf("workspace file changed: %s: %v", name, err)
		}
	}
	for _, name := range []string{"app/go.sum", "lib/go.sum", "replacement/go.sum"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("workspace sum written: %s: %v", name, err)
		}
	}
}

func TestDiagnosticOutputCap(t *testing.T) {
	var b cappedDiagnostic
	block := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 20; i++ {
		if n, err := b.Write(block); n != len(block) || err != nil {
			t.Fatalf("write: %d, %v", n, err)
		}
	}
	if !b.truncated || b.data.Len() != 16<<20 {
		t.Fatalf("uncapped output: len=%d truncated=%v", b.data.Len(), b.truncated)
	}
}
