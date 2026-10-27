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

func TestConfineHidesHostCredentials(t *testing.T) {
	if Unconfined() {
		t.Skip("explicitly unconfined")
	}
	t.Setenv("GO2LEAN_TEST_SECRET", "must-not-reach-agent-code")
	prefix, err := Confine(nil, nil, "")
	if err != nil {
		t.Skip(err)
	}
	args := append(prefix, "/bin/sh", "-c", `test -z "$GO2LEAN_TEST_SECRET" && test "$HOME" = /tmp/home && test -d "$HOME" && echo scrubbed`)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "scrubbed") {
		t.Fatalf("host environment exposed: %v: %s", err, out)
	}
}

func TestConfineHidesHome(t *testing.T) {
	if Unconfined() {
		t.Skip("explicitly unconfined")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := os.CreateTemp(home, "go2lean-lean-private-test-")
	if err != nil {
		t.Fatal(err)
	}
	name := secret.Name()
	secret.Close()
	defer os.Remove(name)
	prefix, err := Confine(nil, nil, "")
	if err != nil {
		t.Skip(err)
	}
	args := append(prefix, "/bin/sh", "-c", `test ! -e "$1" && echo hidden`, "sh", name)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "hidden") {
		t.Fatalf("home visible: %v: %s", err, out)
	}
}
