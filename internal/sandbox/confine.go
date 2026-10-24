package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// ErrNoBwrap says that code which must not run outside a sandbox (Lean
// text an agent wrote) cannot run here: there is no usable bwrap.
var ErrNoBwrap = errors.New("bubblewrap (bwrap) with user namespaces is required to run Lean text an agent wrote; install bubblewrap, or set GO2LEAN_LEAN_SANDBOX=off to run it unconfined (the proof records that it was)")

var (
	bwrapOnce sync.Once
	bwrapOK   bool
)

// Unconfined reports whether the user turned confinement of untrusted Lean
// off (GO2LEAN_LEAN_SANDBOX=off): Confine then returns no prefix, and the
// judge runs comparator without its sandbox. Every proof records it.
func Unconfined() bool { return os.Getenv("GO2LEAN_LEAN_SANDBOX") == "off" }

// Confine returns the command prefix that runs untrusted code (Lean text an
// agent wrote, and the tools it calls) in bubblewrap: the file system
// read-only, the user's home directory hidden except the directories in ro
// (read-only) and rw (writable), a private /tmp, no network, its own
// namespaces, killed with go2lean. Directories under /tmp or the home
// directory must be listed to be seen at all. There is no silent fallback:
// without bwrap it returns ErrNoBwrap, unless Unconfined.
func Confine(ro, rw []string, chdir string) ([]string, error) {
	if Unconfined() {
		return nil, nil
	}
	bwrapOnce.Do(func() {
		bwrapOK = exec.Command("bwrap", "--ro-bind", "/", "/", "--unshare-all", "--die-with-parent", "true").Run() == nil
	})
	if !bwrapOK {
		return nil, ErrNoBwrap
	}
	args := []string{"bwrap", "--ro-bind", "/", "/"}
	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		args = append(args, "--tmpfs", home)
	}
	args = append(args, "--tmpfs", "/tmp", "--dev", "/dev", "--proc", "/proc")
	for _, d := range ro {
		if _, err := os.Stat(d); err == nil {
			args = append(args, "--ro-bind", d, d)
		}
	}
	for _, d := range rw {
		args = append(args, "--bind", d, d)
	}
	if chdir != "" {
		args = append(args, "--chdir", chdir)
	}
	return append(args, "--unshare-all", "--die-with-parent", "--new-session"), nil
}

// LeanReadOnly are the directories untrusted Lean code needs to read: the
// Lean toolchains and go2lean's core library snapshots.
func LeanReadOnly() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dirs := []string{filepath.Join(home, ".elan")}
	if e := os.Getenv("ELAN_HOME"); e != "" {
		dirs = append(dirs, e)
	}
	if c := os.Getenv("GO2LEAN_CACHE"); c != "" {
		dirs = append(dirs, c)
	} else if c, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(c, "go2lean"))
	}
	return dirs
}
