// Package agent is M2's agentic layer: an LLM proposes spec-in-Go properties
// for a package, the pipeline hunts and verifies them, and the LLM turns the
// verdicts into a report. Every prompt and response is written to the
// workspace so a run can be inspected and replayed.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LLM is the one call the agent makes. Implementations: Claude (the
// Anthropic API) and Mock (scripted responses for tests and offline runs).
type LLM interface {
	Name() string
	Complete(ctx context.Context, system, user string) (string, error)
}

// New builds an LLM from a spec: "claude" or "mock:<file-or-dir>". A mock
// directory holds round-1.md, round-2.md, ...; a mock file answers every call.
// cfg applies to "claude" only.
func New(spec string, cfg ClaudeConfig) (LLM, error) {
	switch {
	case spec == "" || spec == "claude":
		if cfg.Model == "" && cfg.BaseURL == "" {
			return NewClaude(), nil
		}
		return NewClaudeWith(cfg), nil
	case strings.HasPrefix(spec, "mock:"):
		return &Mock{Path: strings.TrimPrefix(spec, "mock:")}, nil
	}
	return nil, fmt.Errorf("unknown llm %q (want claude or mock:<path>)", spec)
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
