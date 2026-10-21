package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunIsolation(t *testing.T) {
	if Mode() != ModeBwrap {
		t.Skipf("sandbox mode %s", Mode())
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "in.txt"), []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(dir, "testdata"), 0o755)
	os.WriteFile(filepath.Join(dir, "testdata", "x"), []byte("x"), 0o644)
	outside := filepath.Join(dir, "written")
	sh := `cat in.txt testdata/x; echo ok > here.txt && echo local-ok
echo bad > ` + outside + ` 2>/dev/null || echo outside-blocked
echo bad > /` + filepath.Base(outside) + ` 2>/dev/null || echo root-blocked`
	out, errOut, exit, err := Run("/bin/sh", dir, []string{"-c", sh}, nil, Default)
	if err != nil || exit != 0 {
		t.Fatalf("exit %d err %v stderr %s", exit, err, errOut)
	}
	for _, want := range []string{"hellox", "local-ok", "outside-blocked", "root-blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("the run wrote outside its copy")
	}
	if _, err := os.Stat(filepath.Join(dir, "here.txt")); err == nil {
		t.Errorf("the run wrote into the original directory")
	}
}

func TestRunWallClock(t *testing.T) {
	if Mode() == ModeNone {
		t.Skip("no sandbox")
	}
	lim := Default
	lim.Wall = time.Second
	start := time.Now()
	_, errOut, exit, err := Run("/bin/sh", t.TempDir(), []string{"-c", "sleep 30"}, nil, lim)
	if err != nil {
		t.Fatal(err)
	}
	if exit == 0 || time.Since(start) > 15*time.Second || !strings.Contains(errOut, "wall-clock") {
		t.Errorf("exit %d after %s, stderr %q", exit, time.Since(start), errOut)
	}
}
