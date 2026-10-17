// Command go2lean drives the three modules: compile (M1), props/hunt (M2),
// poc (M3) and run (all three).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/pipeline"
	"github.com/andreasfoo/go2lean/internal/repo"
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
	case "agent":
		err = cmdAgent(os.Args[2:])
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
  go2lean run     [-workspace out] <package>      all of the above
  go2lean agent   [-workspace out] [-llm claude|mock:<file>] <package>
                                                  M2 agent: propose spec-in-Go properties, run, report`)
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
	_, err = pipeline.Compile(w, root, fs.Arg(0), os.Stdout)
	return err
}
