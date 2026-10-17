// Command go2lean drives the three modules: compile (M1), props/hunt (M2),
// poc (M3) and run (all three).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/frontend/gossa"
	"github.com/andreasfoo/go2lean/internal/repo"
	"github.com/andreasfoo/go2lean/internal/ssaout"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "compile":
		err = cmdCompile(os.Args[2:])
	case "props":
		err = cmdProps(os.Args[2:])
	case "hunt":
		err = cmdHunt(os.Args[2:])
	case "poc":
		err = cmdPoc(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "go2lean:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  go2lean compile [-workspace out] <package>      M1: Go -> Lean model, ABI, targets
  go2lean props   [-workspace out]                M2: template properties from the model report
  go2lean hunt    [-workspace out] [-property id] M2: search for counterexamples
  go2lean poc     [-workspace out] -counterexample id   M3: realise and verify
  go2lean run     [-workspace out] <package>      all of the above`)
	os.Exit(2)
}

func workspaceFlag(fs *flag.FlagSet) *string {
	return fs.String("workspace", "out", "workspace directory for JSON records")
}

func openWorkspace(path string) (contract.Workspace, string, error) {
	root, err := repo.Root()
	if err != nil {
		return contract.Workspace{}, "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	w := contract.Workspace{Root: path}
	return w, root, w.Init()
}

func cmdCompile(args []string) error {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	ws := workspaceFlag(fs)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("compile: exactly one package pattern expected")
	}
	w, root, err := openWorkspace(*ws)
	if err != nil {
		return err
	}
	_, err = compile(w, root, fs.Arg(0))
	return err
}

func compile(w contract.Workspace, root, pattern string) (*contract.ModelReport, error) {
	prog, modPath, err := gossa.Translate(root, pattern)
	if err != nil {
		return nil, err
	}
	report, err := ssaout.Emit(prog, repo.LeanDir(root), w, modPath)
	if err != nil {
		return nil, err
	}
	fmt.Printf("compiled %s -> %s\n", prog.Package, report.LeanFile)
	for _, f := range report.Funcs {
		fmt.Printf("  %-12s %-9s %s", f.Name, f.Status, f.GoSig)
		if f.SpecFor != "" {
			fmt.Printf("  [spec for %s]", f.SpecFor)
		}
		fmt.Println()
		for _, is := range f.Issues {
			fmt.Printf("      - %s\n", is)
		}
	}
	return report, nil
}
