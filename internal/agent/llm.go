// Package agent is M2's agentic layer: an LLM proposes spec-in-Go properties
// for a package, the pipeline hunts and verifies them, and the LLM turns the
// verdicts into a report. Every prompt and response is written to the
// workspace so a run can be inspected and replayed.
package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LLM is the one call the agent makes. Implementations: Claude (the
// Anthropic API) and Mock (scripted responses for tests and offline runs).
type LLM interface {
	Name() string
	Complete(ctx context.Context, system, user string) (string, error)
}

// New builds an LLM from a spec: "claude", "claude-code" (the claude CLI,
// with its own login) or "mock:<file-or-dir>". A mock
// directory holds round-1.md, round-2.md, ...; a mock file answers every call.
// cfg applies to "claude" only.
func New(spec string, cfg ClaudeConfig) (LLM, error) {
	switch {
	case spec == "" || spec == "claude":
		if cfg.Model == "" && cfg.BaseURL == "" {
			return NewClaude(), nil
		}
		return NewClaudeWith(cfg), nil
	case spec == "claude-code":
		effort := ""
		if cfg.Thinking {
			effort = "high"
		}
		return &ClaudeCode{Model: cfg.Model, Effort: effort}, nil
	case strings.HasPrefix(spec, "mock:"):
		return &Mock{Path: strings.TrimPrefix(spec, "mock:")}, nil
	}
	return nil, fmt.Errorf("unknown llm %q (want claude, claude-code or mock:<path>)", spec)
}

// Mock replays scripted responses.
type Mock struct {
	Path  string
	calls int
}

func (m *Mock) Name() string { return "mock:" + m.Path }

func (m *Mock) Complete(_ context.Context, _, _ string) (string, error) {
	m.calls++
	info, err := os.Stat(m.Path)
	if err != nil {
		return "", err
	}
	path := m.Path
	if info.IsDir() {
		path = filepath.Join(m.Path, fmt.Sprintf("round-%d.md", m.calls))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("mock response for call %d: %w", m.calls, err)
	}
	return string(b), nil
}

// ClaudeCode answers through the claude CLI (`claude -p`), with no tools
// and the given system prompt in place of its own: a completion, on the
// CLI's login rather than an API key.
type ClaudeCode struct {
	Model  string
	Effort string // --effort: low, medium, high, xhigh, max; empty: the CLI's default
}

func (c *ClaudeCode) Name() string {
	if c.Model == "" {
		return "claude-code"
	}
	return "claude-code:" + c.Model
}

func (c *ClaudeCode) Complete(ctx context.Context, system, user string) (string, error) {
	args := []string{"-p", "--tools", "", "--system-prompt", system, "--no-session-persistence", "--output-format", "text"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	if c.Effort != "" {
		args = append(args, "--effort", c.Effort)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdin = strings.NewReader(user)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if strings.TrimSpace(string(out)) == "" {
		return "", fmt.Errorf("claude -p: empty answer")
	}
	return string(out), nil
}
