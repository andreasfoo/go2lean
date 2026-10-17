package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ClaudeConfig selects the model and endpoint. The zero value is Claude
// Opus 5.5 on the Anthropic API with adaptive thinking; BaseURL points the
// same client at an Anthropic-compatible endpoint of another provider, in
// which case Thinking is usually off because the parameter is not portable.
type ClaudeConfig struct {
	Model    string
	BaseURL  string
	Thinking bool
}

// Claude calls the Anthropic Messages API. Credentials come from the
// environment (ANTHROPIC_API_KEY, or an `ant auth login` profile).
type Claude struct {
	client anthropic.Client
	cfg    ClaudeConfig
}

func NewClaude() *Claude {
	return NewClaudeWith(ClaudeConfig{Model: "claude-opus-5-5", Thinking: true})
}

func NewClaudeWith(cfg ClaudeConfig) *Claude {
	if cfg.Model == "" {
		cfg.Model = "claude-opus-5-5"
	}
	var opts []option.RequestOption
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return &Claude{client: anthropic.NewClient(opts...), cfg: cfg}
}

func (c *Claude) Name() string { return c.cfg.Model }

func (c *Claude) Complete(ctx context.Context, system, user string) (string, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.cfg.Model),
		MaxTokens: 16000,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	}
	if c.cfg.Thinking {
		adaptive := anthropic.ThinkingConfigAdaptiveParam{}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	}
	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return "", err
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", fmt.Errorf("model refused the request (%s)", resp.StopDetails.Category)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), nil
}
