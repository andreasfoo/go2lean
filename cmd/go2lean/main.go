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
	limitMemory()
	startMemProfile()
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
	case "summary":
		err = cmdSummary(os.Args[2:])
	case "llvm":
		err = cmdLLVM(os.Args[2:])
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
  common flags: -workspace out   -module <dir of the Go module to analyse>
  go2lean compile [-workspace out] <packages...>  M1: Go -> Lean model, ABI, targets (./... allowed)
  go2lean props   [-workspace out]                M2: template properties from the model report
  go2lean hunt    [-workspace out] [-property id] M2: search for counterexamples
  go2lean poc     [-workspace out] -counterexample id   M3: realise and verify
  go2lean run     [-workspace out] <package>      all of the above
  go2lean summary [-workspace out]                 all verdicts and proofs of a workspace as Markdown
  go2lean agent   [-workspace out] [-llm claude|mock:<file>] <package>
                                                  M2 agent: propose spec-in-Go properties, run, report
  go2lean llvm    [-workspace out] <file.c|.ll|.bc> C / LLVM IR: model every function, search for UB,
                                                  replay counterexamples under ASan/UBSan (build with -tags llvm18)`)
	os.Exit(2)
}

// commonFlags are shared by every subcommand: where records go and which Go
// module is analysed.
type commonFlags struct {
	workspace *string
	module    *string
}

func workspaceFlag(fs *flag.FlagSet) *commonFlags {
	return &commonFlags{
		workspace: fs.String("workspace", "out", "workspace directory for JSON records"),
		module:    fs.String("module", "", "directory of the Go module to analyse (default: this checkout)"),
	}
}

// openWorkspace resolves the workspace and the environment. The Lean project
// always lives in the go2lean checkout; -module points package loading and
// go test at another Go module.
func openWorkspace(cf *commonFlags) (contract.Workspace, pipeline.Env, error) {
	root, err := repo.Root()
	if err != nil {
		return contract.Workspace{}, pipeline.Env{}, err
	}
	path := *cf.workspace
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	env := pipeline.Env{Root: root}
	if *cf.module != "" {
		if env.Module, err = filepath.Abs(*cf.module); err != nil {
			return contract.Workspace{}, pipeline.Env{}, err
		}
	}
	w := contract.Workspace{Root: path}
	return w, env, w.Init()
}

func cmdCompile(args []string) error {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	ws := workspaceFlag(fs)
	deps := fs.Bool("deps", false, "also model the functions of dependency packages the compiled packages call")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("compile: at least one package pattern expected (e.g. ./...)")
	}
	w, env, err := openWorkspace(ws)
	if err != nil {
		return err
	}
	env.Deps = *deps
	_, err = pipeline.CompileAll(w, env, os.Stdout, fs.Args()...)
	return err
}
