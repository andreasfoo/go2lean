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
	model := fs.String("model", "", "model id for -llm claude (default claude-opus-5-5)")
	baseURL := fs.String("base-url", "", "Anthropic-compatible endpoint for -llm claude (default: the Anthropic API)")
	thinking := fs.Bool("thinking", true, "send adaptive thinking (turn off for providers that reject the parameter)")
	repairs := fs.Int("repairs", 2, "rounds spent repairing specs the frontend rejected")
	report := fs.Bool("report", true, "ask the agent for a report (and possibly revised specs) after the verdicts")
	guidance := fs.String("guidance", "", "file with guidance for the proposal round (bug classes, invariants, functions to target)")
	opts := searchFlags(fs)
	fs.Parse(args)
	guidanceText := ""
	if *guidance != "" {
		b, err := os.ReadFile(*guidance)
		if err != nil {
			return err
		}
		guidanceText = string(b)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("agent: exactly one package pattern expected")
	}
	w, env, err := openWorkspace(ws)
	if err != nil {
		return err
	}
	llm, err := agent.New(*llmSpec, agent.ClaudeConfig{Model: *model, BaseURL: *baseURL, Thinking: *thinking})
	if err != nil {
		return err
	}
	return agent.Run(context.Background(), w, env, fs.Arg(0), llm, agent.Options{
		Search: opts.get(), MaxRepairs: *repairs, ReportRound: *report, Guidance: guidanceText,
	}, os.Stdout)
}
