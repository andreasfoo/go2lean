package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/andreasfoo/go2lean/internal/sandbox"
	leanassets "github.com/andreasfoo/go2lean/lean"
)

// doctor inspects prerequisites without downloading a toolchain, building a
// model, loading a target package, or executing target code.
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("doctor: no positional arguments expected")
	}
	return diagnose(os.Stdout)
}

func diagnose(out io.Writer) error {
	failed := false
	check := func(ok bool, name, detail string) {
		status := "ok"
		if !ok {
			status = "FAIL"
			failed = true
		}
		fmt.Fprintf(out, "%-5s %-15s %s\n", status, name, detail)
	}
	for _, tool := range []string{"go", "elan", "lake", "prlimit"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			check(false, tool, "not on PATH; install it and add its bin directory to PATH")
		} else {
			check(true, tool, p)
		}
	}
	pinned, err := leanassets.Core.ReadFile("lean-toolchain")
	if err != nil {
		return err
	}
	pin := strings.TrimSpace(string(pinned))
	// `elan toolchain list` is local only; invoking lake --version could fetch
	// a missing default toolchain or use the caller's lean-toolchain file.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listing, err := exec.CommandContext(ctx, "elan", "toolchain", "list").CombinedOutput()
	installed := false
	if err == nil {
		for _, line := range strings.Split(string(listing), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] == pin {
				installed = true
			}
		}
	}
	detail := pin
	if !installed {
		detail += "; run: elan toolchain install " + pin
	}
	check(installed, "Lean toolchain", detail)
	mode := sandbox.Mode()
	check(mode == sandbox.ModeBwrap, "replay isolation", mode+" (requires working bubblewrap/user namespaces and prlimit)")
	if sandbox.Unconfined() {
		check(false, "Lean isolation", "GO2LEAN_LEAN_SANDBOX=off disables proof confinement")
	} else {
		_, err := sandbox.Confine(nil, nil, "")
		check(err == nil, "Lean isolation", "bubblewrap/user namespaces required for untrusted Lean")
	}
	fmt.Fprintln(out, "Scope: checks local prerequisites only; does not validate target dependencies or promise that arbitrary target code is safe.")
	if failed {
		return fmt.Errorf("doctor: prerequisites missing or isolation disabled; see diagnostics above")
	}
	return nil
}
