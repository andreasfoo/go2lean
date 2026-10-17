package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// Claude calls the Anthropic Messages API. Credentials come from the
// environment (ANTHROPIC_API_KEY, or an `ant auth login` profile). The SDK
// also honours ANTHROPIC_BASE_URL, so Anthropic-compatible gateways work;
// GO2LEAN_MODEL overrides the model name for them.
type Claude struct {
	client anthropic.Client
	model  string
}

func NewClaude() *Claude {
	model := "claude-opus-5-5"
	if m := os.Getenv("GO2LEAN_MODEL"); m != "" {
		model = m
	}
	return &Claude{client: anthropic.NewClient(), model: model}
}

func (c *Claude) Name() string { return c.model }

func (c *Claude) Complete(ctx context.Context, system, user string) (string, error) {
	adaptive := anthropic.ThinkingConfigAdaptiveParam{}
	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 16000,
		Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive},
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
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
