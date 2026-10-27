package sandbox

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestConfinePrivateTemp(t *testing.T) {
	if Unconfined() {
		t.Skip("explicitly unconfined")
	}
	t.Setenv("TMPDIR", "/hidden-host-tmp")
	prefix, err := Confine(nil, nil, "")
	if err != nil {
		t.Skip(err)
	}
	args := append(prefix, "/bin/sh", "-c", `test "$TMPDIR" = /tmp && f=$(mktemp) && echo private && rm "$f"`)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "private") {
		t.Fatalf("private tmp: %v: %s", err, out)
	}
}
