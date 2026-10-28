package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

// cappedDiagnostic keeps a broken tool or enormous package graph from filling
// memory. Writes still succeed so os/exec can finish draining the child.
type cappedDiagnostic struct {
	data      bytes.Buffer
	truncated bool
}

func (b *cappedDiagnostic) String() string { return b.data.String() }

func (b *cappedDiagnostic) Write(p []byte) (int, error) {
	const limit = 16 << 20
	n := len(p)
	if len(p) > limit-b.data.Len() {
		p = p[:limit-b.data.Len()]
		b.truncated = true
	}
	_, _ = b.data.Write(p)
	return n, nil
}

// diagnoseDeps inspects the locally available package graph, not type safety
// or model support. In particular, it must not turn a missing cache entry into
// a network download, rewrite module files, or honor host GOFLAGS build hooks.
func diagnoseDeps(out io.Writer, patterns []string) error {
	for _, p := range patterns {
		if p == "" || strings.HasPrefix(p, "-") || strings.Contains(p, "@") {
			return fmt.Errorf("doctor: expected local package patterns, not flags or version queries: %q", p)
		}
	}
	work, cleanup, err := doctorWorkspace()
	if err != nil {
		fmt.Fprintf(out, "FAIL  dependencies    %v\n", err)
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append([]string{"list", "-mod=readonly", "-e", "-deps", "-test", "-json=ImportPath,Error,DepsErrors,Incomplete", "--"}, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	overrides := []string{"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOVCS=*:off", "GOFLAGS=", "GOENV=off", "GOTELEMETRY=off", "GOWORK=" + work}
	blocked := map[string]bool{}
	for _, entry := range overrides {
		blocked[strings.SplitN(entry, "=", 2)[0]] = true
	}
	for _, entry := range os.Environ() {
		if !blocked[strings.SplitN(entry, "=", 2)[0]] {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, overrides...)
	var stdout, stderr cappedDiagnostic
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		runErr = ctx.Err()
	}
	err = reportDeps(out, &stdout.data, stderr.String(), runErr)
	if stdout.truncated || stderr.truncated {
		fmt.Fprintln(out, "FAIL  dependencies    diagnostic output exceeded 16 MiB; use narrower package patterns")
		return fmt.Errorf("doctor: dependency diagnostic output truncated")
	}
	return err
}

// Go's -mod=readonly protects module sums, but workspace mode may still write
// go.work.sum. Inspect a disposable workspace manifest with absolute local
// paths instead, preserving workspace resolution without modifying its sums.
func doctorWorkspace() (string, func(), error) {
	noop := func() {}
	path := os.Getenv("GOWORK")
	if path == "off" {
		return "off", noop, nil
	}
	if path == "" || path == "auto" {
		dir, err := os.Getwd()
		if err != nil {
			return "", noop, err
		}
		for {
			candidate := filepath.Join(dir, "go.work")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			} else if !os.IsNotExist(err) {
				return "", noop, err
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return "off", noop, nil
			}
			dir = parent
		}
	}
	if !filepath.IsAbs(path) {
		return "", noop, fmt.Errorf("doctor: GOWORK must be an absolute path or off")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", noop, err
	}
	work, err := modfile.ParseWork(path, data, nil)
	if err != nil {
		return "", noop, err
	}
	base := filepath.Dir(path)
	for _, use := range append([]*modfile.Use(nil), work.Use...) {
		if !filepath.IsAbs(use.Path) {
			diskPath, modulePath := filepath.Join(base, use.Path), use.ModulePath
			if err := work.DropUse(use.Path); err != nil {
				return "", noop, err
			}
			if err := work.AddUse(diskPath, modulePath); err != nil {
				return "", noop, err
			}
		}
	}
	for _, r := range work.Replace {
		if r.New.Version == "" && !filepath.IsAbs(r.New.Path) {
			if err := work.AddReplace(r.Old.Path, r.Old.Version, filepath.Join(base, r.New.Path), ""); err != nil {
				return "", noop, err
			}
		}
	}
	work.Cleanup()
	dir, err := os.MkdirTemp("", "go2lean-doctor-work-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	dest := filepath.Join(dir, "go.work")
	if err := os.WriteFile(dest, modfile.Format(work.Syntax), 0o600); err != nil {
		cleanup()
		return "", noop, err
	}
	sums, err := os.ReadFile(path + ".sum")
	if err == nil {
		err = os.WriteFile(dest+".sum", sums, 0o600)
	}
	if err != nil && !os.IsNotExist(err) {
		cleanup()
		return "", noop, err
	}
	return dest, cleanup, nil
}

func reportDeps(out io.Writer, src io.Reader, stderr string, runErr error) error {
	if runErr != nil {
		fmt.Fprintf(out, "FAIL  dependencies    go list failed: %v\n%s\n", runErr, strings.TrimSpace(stderr))
		return fmt.Errorf("doctor: target dependency inspection failed: %w", runErr)
	}
	type loadError struct {
		ImportStack []string
		Pos         string
		Err         string
	}
	decoder := json.NewDecoder(src)
	count, failures := 0, 0
	seen := map[string]bool{}
	for {
		var pkg struct {
			ImportPath string
			Incomplete bool
			Error      *loadError
			DepsErrors []loadError
		}
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil || pkg.ImportPath == "" {
			fmt.Fprintln(out, "FAIL  dependencies    malformed go list output")
			return fmt.Errorf("doctor: invalid dependency inspection output")
		}
		count++
		issues := pkg.DepsErrors
		if pkg.Error != nil {
			issues = append(issues, *pkg.Error)
		}
		if pkg.Incomplete && len(issues) == 0 {
			issues = append(issues, loadError{Err: "incomplete package: " + pkg.ImportPath})
		}
		for i, issue := range issues {
			message := issue.Err
			if issue.Pos != "" {
				message = issue.Pos + ": " + message
			}
			if len(issue.ImportStack) > 0 {
				message += " [import stack: " + strings.Join(issue.ImportStack, " -> ") + "]"
			}
			if !seen[message] {
				seen[message] = true
				failures++
				if pkg.Error != nil && i == len(pkg.DepsErrors) {
					message = pkg.ImportPath + ": " + message
				}
				fmt.Fprintf(out, "FAIL  dependency      %s\n", message)
			}
		}
	}
	if count == 0 {
		fmt.Fprintf(out, "FAIL  dependencies    no packages matched\n%s\n", strings.TrimSpace(stderr))
		return fmt.Errorf("doctor: no target packages matched")
	}
	if strings.Contains(stderr, "matched no packages") {
		fmt.Fprintf(out, "FAIL  dependencies    at least one requested pattern matched no packages\n%s\n", strings.TrimSpace(stderr))
		return fmt.Errorf("doctor: incomplete target package selection")
	}
	if failures > 0 {
		fmt.Fprintln(out, "Resolve dependencies with trusted tooling in a network-enabled setup, then rerun doctor -deps. No modules or toolchains were downloaded by this check.")
		return fmt.Errorf("doctor: target dependency errors (%d distinct diagnostics)", failures)
	}
	fmt.Fprintf(out, "ok    dependencies    %d locally available packages (offline metadata only; no build, type check or model-support guarantee)\n", count)
	if strings.TrimSpace(stderr) != "" {
		fmt.Fprintf(out, "note  go list         %s\n", strings.TrimSpace(stderr))
	}
	return nil
}
