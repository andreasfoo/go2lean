package builtin

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceFingerprintIncludesTestsAndEmbedDataWithoutExecution(t *testing.T) {
	dir := fixture(t, `import ("embed"; "os")
	var _ embed.FS
	//go:embed payload.txt
	var payload string
	func init() { os.WriteFile("target-executed", nil, 0600) }
	func Compare(a,b string) int { return 0 }
	`)
	if err := os.WriteFile(filepath.Join(dir, "payload.txt"), []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := CaptureSources(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "target-executed")); !os.IsNotExist(err) {
		t.Fatal("metadata executed target init")
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.txt"), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := CaptureSources(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if first.Equal(changed) {
		t.Fatal("embedded data change missed")
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), []byte("package fixture\nimport _ \"example.invalid/missing\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureSources(dir, "."); err == nil {
		t.Fatal("missing test dependency accepted")
	}
}

func TestSourceFingerprintRefusesImplicitBuildConfigurations(t *testing.T) {
	dir := fixture(t, ordered)
	t.Setenv("GOFLAGS", "-tags=custom")
	if _, err := CaptureSources(dir, "."); err == nil {
		t.Fatal("custom build flags silently ignored")
	}
	t.Setenv("GOFLAGS", "")
	work := filepath.Join(t.TempDir(), "go.work")
	if err := os.WriteFile(work, []byte("go 1.26.0\nuse "+dir+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", work)
	if _, err := CaptureSources(dir, "."); err == nil {
		t.Fatal("active workspace silently ignored")
	}
}

func TestSourceProbeOutputBound(t *testing.T) {
	b := probeBuffer{limit: 64}
	input := bytes.Repeat([]byte("x"), 128)
	if n, err := b.Write(input); n != len(input) || err != nil || b.Len() != 64 || !b.exceeded {
		t.Fatalf("metadata output not bounded: n=%d err=%v size=%d", n, err, b.Len())
	}
	if n, err := b.Write(input); n != len(input) || err != nil || b.Len() != 64 {
		t.Fatal("repeated writes not drained safely")
	}
}

func TestSourceFingerprintRefusesCrossCompilation(t *testing.T) {
	dir := fixture(t, ordered)
	t.Setenv("GOARCH", "386")
	if _, err := CaptureSources(dir, "."); err == nil {
		t.Fatal("unsupported 32-bit/cross compilation accepted")
	}
}
