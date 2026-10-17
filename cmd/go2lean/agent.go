package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/andreasfoo/go2lean/internal/agent"
)

func cmdAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	ws := workspaceFlag(fs)
	llmSpec := fs.String("llm", "claude", "LLM to use: claude, or mock:<file-or-dir> with scripted responses")
	repairs := fs.Int("repairs", 2, "rounds spent repairing specs the frontend rejected")
	report := fs.Bool("report", true, "ask the agent for a report (and possibly revised specs) after the verdicts")
	opts := searchFlags(fs)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("agent: exactly one package pattern expected")
	}
	w, root, err := openWorkspace(*ws)
	if err != nil {
		return err
	}
	llm, err := agent.New(*llmSpec)
	if err != nil {
		return err
	}
	return agent.Run(context.Background(), w, root, fs.Arg(0), llm, agent.Options{
		Search: opts.get(), MaxRepairs: *repairs, ReportRound: *report,
	}, os.Stdout)
}
