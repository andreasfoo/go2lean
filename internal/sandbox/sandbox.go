// Package sandbox runs the code go2lean generates (test binaries that replay
// counterexamples on the user's code) away from the user's files and network,
// under resource limits.
//
// Generated tests are added to a package through `go test -overlay`, so
// nothing is written into the package directory, and built with `-c`. The
// binary then runs in a fresh copy of the package directory (its files and
// testdata). Where user namespaces are available (`unshare -r`), the run has
// no network and every mount is read-only except that copy; elsewhere only
// the resource limits apply, and Mode says so.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Limits bound one run.
type Limits struct {
	MemBytes   int64 // address space; 0 for none (ASan reserves terabytes)
	CPUSeconds int64
	FileBytes  int64 // largest file the run may write
	Wall       time.Duration
}

// Default is generous for a unit test and stops runaway ones (a random
// input that allocates gigabytes, a loop that never ends).
var Default = Limits{MemBytes: 8 << 30, CPUSeconds: 300, FileBytes: 256 << 20, Wall: 10 * time.Minute}

const (
	// ModeNamespaces: no network, read-only file system except the run's
	// copy of the package, resource limits.
	ModeNamespaces = "namespaces"
	// ModeLimits: resource limits only (no user namespaces here).
	ModeLimits = "rlimits"
	// ModeNone: GO2LEAN_SANDBOX=off, or neither tool is installed.
	ModeNone = "none"
)

var (
	modeOnce sync.Once
	mode     string
)

// Mode reports the isolation runs get on this machine (detected once).
func Mode() string {
	modeOnce.Do(func() {
		mode = detect()
		if mode != ModeNamespaces {
			fmt.Fprintf(os.Stderr, "go2lean: sandbox: %s (no user namespaces): generated tests can reach the network and the file system; run on a scratch clone\n", mode)
		}
	})
	return mode
}

func detect() string {
	if os.Getenv("GO2LEAN_SANDBOX") == "off" {
		return ModeNone
	}
	if _, err := exec.LookPath("prlimit"); err != nil {
		return ModeNone
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		return ModeLimits
	}
	w, err := os.MkdirTemp("", "go2lean-sbx-probe-")
	if err != nil {
		return ModeLimits
	}
	defer os.RemoveAll(w)
	cmd := exec.Command("unshare", "-r", "-n", "-m", "--propagation", "private", "sh", "-c", script, "sh", w, "true")
	if cmd.Run() != nil {
		return ModeLimits
	}
	return ModeNamespaces
}

// script runs inside the new namespaces: keep the work directory writable,
// make every other mount read-only, then run the command from the copy.
const script = `w=$1; shift
mount --bind "$w" "$w" || exit 125
awk '{print $2}' /proc/self/mounts | while read -r m; do
  case "$m" in /proc|/proc/*|/sys|/sys/*|/dev|/dev/*|"$w") ;; *) mount -o remount,bind,ro "$m" 2>/dev/null ;; esac
done
mkdir -p "$w/pkg" && cd "$w/pkg" || exit 125
exec "$@"`

// BuildTest compiles pkg's tests plus the given files (base name to
// source, placed in pkgDir through an overlay) into the binary out. The
// package directory is not written. stderr holds the compiler's errors.
func BuildTest(root, pkg, pkgDir string, files map[string][]byte, out string) (stderr string, exit int, err error) {
	tmp, err := os.MkdirTemp("", "go2lean-overlay-")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(tmp)
	overlay := map[string]map[string]string{"Replace": {}}
	for name, src := range files {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, src, 0o644); err != nil {
			return "", 0, err
		}
		overlay["Replace"][filepath.Join(pkgDir, name)] = p
	}
	ov, _ := json.Marshal(overlay)
	ovPath := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(ovPath, ov, 0o644); err != nil {
		return "", 0, err
	}
	cmd := exec.Command("go", "test", "-c", "-overlay", ovPath, "-o", out, pkg)
	cmd.Dir = root
	var eb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &eb, &eb
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return eb.String(), ee.ExitCode(), nil
	}
	return eb.String(), 0, err
}

// Run runs bin with args in a fresh copy of dir (its regular files and its
// testdata directory; an empty dir gives an empty one) under the machine's
// Mode and the limits. env adds to a minimal environment.
func Run(bin, dir string, args, env []string, lim Limits) (stdout, stderr string, exit int, err error) {
	w, err := os.MkdirTemp("", "go2lean-sbx-")
	if err != nil {
		return "", "", 0, err
	}
	defer os.RemoveAll(w)
	if err := os.MkdirAll(filepath.Join(w, "pkg"), 0o755); err != nil {
		return "", "", 0, err
	}
	if dir != "" {
		if err := copyPackage(dir, filepath.Join(w, "pkg")); err != nil {
			return "", "", 0, err
		}
	}
	for _, d := range []string{"home", "tmp"} {
		os.MkdirAll(filepath.Join(w, d), 0o755)
	}
	limited := []string{"prlimit"}
	if lim.MemBytes > 0 {
		limited = append(limited, "--as="+strconv.FormatInt(lim.MemBytes, 10))
	}
	limited = append(limited,
		"--cpu="+strconv.FormatInt(lim.CPUSeconds, 10),
		"--fsize="+strconv.FormatInt(lim.FileBytes, 10),
		"--", bin)
	limited = append(limited, args...)
	ctx, cancel := context.WithTimeout(context.Background(), lim.Wall)
	defer cancel()
	var cmd *exec.Cmd
	switch Mode() {
	case ModeNamespaces:
		cmd = exec.CommandContext(ctx, "unshare", append([]string{"-r", "-n", "-m", "--propagation", "private", "sh", "-c", script, "sh", w}, limited...)...)
	case ModeLimits:
		cmd = exec.CommandContext(ctx, limited[0], limited[1:]...)
		cmd.Dir = filepath.Join(w, "pkg")
	default:
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Dir = filepath.Join(w, "pkg")
	}
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(w, "home"),
		"TMPDIR=" + filepath.Join(w, "tmp"),
		"LANG=C.UTF-8",
	}
	cmd.Env = append(cmd.Env, env...)
	// Kill the whole process group on timeout (a test may fork).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var ob, eb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &ob, &eb
	err = cmd.Run()
	if ctx.Err() != nil {
		fmt.Fprintf(&eb, "\ngo2lean: sandbox: killed after the wall-clock limit (%s)\n", lim.Wall)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ob.String(), eb.String(), ee.ExitCode(), nil
	}
	if err != nil && ctx.Err() != nil {
		return ob.String(), eb.String(), -1, nil
	}
	return ob.String(), eb.String(), 0, err
}

// copyPackage copies dir's regular files and its testdata tree to dst.
func copyPackage(dir, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		src := filepath.Join(dir, e.Name())
		switch {
		case e.Type().IsRegular():
			if err := copyFile(src, filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		case e.IsDir() && e.Name() == "testdata":
			err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(dir, p)
				if d.IsDir() {
					return os.MkdirAll(filepath.Join(dst, rel), 0o755)
				}
				if d.Type().IsRegular() {
					return copyFile(p, filepath.Join(dst, rel))
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
