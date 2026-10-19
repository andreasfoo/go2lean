package nonstream

import (
	"github.com/anthropics/anthropic-sdk-go"

	"github.com/tingly-dev/tingly-box/internal/protocol/wire"
)

// Hand-written go2lean specs for the non-streaming Anthropic -> OpenAI Chat
// response converter: block counts, text and the stop reason must survive.

// go2lean:spec ConvertAnthropicBetaToOpenAIChat
// SpecConvertAnthropicBetaToOpenAIChatToolCallCount: one tool_call per
// tool_use block, in order, with the same id and name.
func SpecConvertAnthropicBetaToOpenAIChatToolCallCount(bm *anthropic.BetaMessage, responseModel string, ret wire.ChatCompletionWire) bool {
	if bm == nil {
		return true
	}
	if len(ret.Choices) != 1 {
		return false
	}
	calls := ret.Choices[0].Message.ToolCalls
	n := 0
	for _, block := range bm.Content {
		if block.Type != "tool_use" {
			continue
		}
		if n >= len(calls) || calls[n].ID != block.ID || calls[n].Function.Name != block.Name {
			return false
		}
		n++
	}
	return n == len(calls)
}

// go2lean:spec ConvertAnthropicBetaToOpenAIChat
// SpecConvertAnthropicBetaToOpenAIChatTextKept: the message content is the
// concatenation of the text blocks (checked by length).
func SpecConvertAnthropicBetaToOpenAIChatTextKept(bm *anthropic.BetaMessage, responseModel string, ret wire.ChatCompletionWire) bool {
	if bm == nil || len(ret.Choices) != 1 {
		return true
	}
	total := 0
	for _, block := range bm.Content {
		if block.Type == "text" {
			total += len(block.Text)
		}
	}
	return len(ret.Choices[0].Message.Content) == total
}

// go2lean:spec ConvertAnthropicBetaToOpenAIChat
// SpecConvertAnthropicBetaToOpenAIChatFinishReason: max_tokens maps to
// length, tool_use to tool_calls, and the role is carried over.
func SpecConvertAnthropicBetaToOpenAIChatFinishReason(bm *anthropic.BetaMessage, responseModel string, ret wire.ChatCompletionWire) bool {
	if bm == nil || len(ret.Choices) != 1 {
		return true
	}
	fr := ret.Choices[0].FinishReason
	if bm.StopReason == "max_tokens" && fr != "length" {
		return false
	}
	if bm.StopReason == "tool_use" && fr != "tool_calls" {
		return false
	}
	return ret.Choices[0].Message.Role == string(bm.Role) && ret.Model == responseModel && ret.ID == bm.ID
}
