package usage

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"

	protocol "github.com/tingly-dev/tingly-box/ai"
	"github.com/tingly-dev/tingly-box/internal/protocol/wire"
)

// Hand-written go2lean specs for the usage normalisation rules stated at the
// top of extract.go: prompt totals are preserved across the normalised form
// and cache counts never move between the input and the output side.

func nonNegativeChat(u openai.CompletionUsage) bool {
	return u.PromptTokens >= 0 && u.CompletionTokens >= 0 &&
		u.PromptTokensDetails.CachedTokens >= 0 && u.PromptTokensDetails.CacheWriteTokens >= 0 &&
		u.CompletionTokensDetails.ReasoningTokens >= 0 &&
		u.PromptTokensDetails.CachedTokens <= u.PromptTokens
}

// go2lean:spec FromOpenAIChatCompletion
// SpecFromOpenAIChatCompletionPromptTotal: the normalised input plus cache
// reads equals the wire prompt total, and the other counts are copied.
func SpecFromOpenAIChatCompletionPromptTotal(u openai.CompletionUsage, ret *protocol.TokenUsage) bool {
	if ret == nil || !nonNegativeChat(u) {
		return true
	}
	return ret.InputTokens+ret.CacheReadTokens == int(u.PromptTokens) &&
		ret.OutputTokens == int(u.CompletionTokens) &&
		ret.CacheReadTokens == int(u.PromptTokensDetails.CachedTokens) &&
		ret.CacheWriteTokens == int(u.PromptTokensDetails.CacheWriteTokens) &&
		ret.ReasoningTokens == int(u.CompletionTokensDetails.ReasoningTokens)
}

// go2lean:spec FromOpenAIChatCompletion
// SpecFromOpenAIChatCompletionRoundTrip: converting back with ChatUsage
// restores the wire counts.
func SpecFromOpenAIChatCompletionRoundTrip(u openai.CompletionUsage, ret *protocol.TokenUsage) bool {
	if ret == nil || !nonNegativeChat(u) {
		return true
	}
	cu := ChatUsage(ret)
	return cu.PromptTokens == u.PromptTokens &&
		cu.CompletionTokens == u.CompletionTokens &&
		cu.TotalTokens == u.PromptTokens+u.CompletionTokens &&
		cu.PromptTokensDetails.CachedTokens == u.PromptTokensDetails.CachedTokens &&
		cu.PromptTokensDetails.CacheWriteTokens == u.PromptTokensDetails.CacheWriteTokens &&
		cu.CompletionTokensDetails.ReasoningTokens == u.CompletionTokensDetails.ReasoningTokens
}

// go2lean:spec FromAnthropicBetaMessage
// SpecFromAnthropicBetaMessageFolds: cache creation is folded into the input
// count and reported as the cache write count; reads are copied.
func SpecFromAnthropicBetaMessageFolds(u anthropic.BetaUsage, ret *protocol.TokenUsage) bool {
	if ret == nil {
		return true
	}
	return ret.InputTokens == int(u.InputTokens)+int(u.CacheCreationInputTokens) &&
		ret.CacheWriteTokens == int(u.CacheCreationInputTokens) &&
		ret.CacheReadTokens == int(u.CacheReadInputTokens) &&
		ret.OutputTokens == int(u.OutputTokens) &&
		ret.ReasoningTokens == int(u.OutputTokensDetails.ThinkingTokens)
}

func nonNegativeUsage(u *protocol.TokenUsage) bool {
	return u.InputTokens >= 0 && u.OutputTokens >= 0 && u.CacheReadTokens >= 0 &&
		u.CacheWriteTokens >= 0 && u.ReasoningTokens >= 0
}

// go2lean:spec ChatUsage
// SpecChatUsageTotals: prompt_tokens is the prompt total, total_tokens is
// prompt plus completion, and the cache details are the normalised counts.
func SpecChatUsageTotals(u *protocol.TokenUsage, ret openai.CompletionUsage) bool {
	if u == nil || !nonNegativeUsage(u) {
		return true
	}
	return ret.PromptTokens == int64(u.InputTokens+u.CacheReadTokens) &&
		ret.CompletionTokens == int64(u.OutputTokens) &&
		ret.TotalTokens == ret.PromptTokens+ret.CompletionTokens &&
		ret.PromptTokensDetails.CachedTokens == int64(u.CacheReadTokens) &&
		ret.PromptTokensDetails.CacheWriteTokens == int64(u.CacheWriteTokens) &&
		ret.CompletionTokensDetails.ReasoningTokens == int64(u.ReasoningTokens)
}

// go2lean:spec ToChatUsageWire
// SpecToChatUsageWireTotals: same totals on the non-streaming Chat wire
// shape; the cache breakdown is present exactly when there is one.
func SpecToChatUsageWireTotals(u *protocol.TokenUsage, ret wire.ChatCompletionUsageWire) bool {
	if u == nil || !nonNegativeUsage(u) {
		return true
	}
	if ret.PromptTokens != int64(u.InputTokens+u.CacheReadTokens) ||
		ret.CompletionTokens != int64(u.OutputTokens) ||
		ret.TotalTokens != ret.PromptTokens+ret.CompletionTokens {
		return false
	}
	if u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
		return ret.PromptTokensDetails != nil &&
			ret.PromptTokensDetails.CachedTokens == int64(u.CacheReadTokens) &&
			ret.PromptTokensDetails.CacheWriteTokens == int64(u.CacheWriteTokens)
	}
	return ret.PromptTokensDetails == nil
}

// go2lean:spec ToChatStreamUsageWire
// SpecToChatStreamUsageWireTotals: same totals on the streaming Chat wire
// shape.
func SpecToChatStreamUsageWireTotals(u *protocol.TokenUsage, ret *wire.ChatStreamUsage) bool {
	if u == nil || !nonNegativeUsage(u) {
		return true
	}
	if ret == nil {
		return false
	}
	if ret.PromptTokens != int64(u.InputTokens+u.CacheReadTokens) ||
		ret.CompletionTokens != int64(u.OutputTokens) ||
		ret.TotalTokens != ret.PromptTokens+ret.CompletionTokens {
		return false
	}
	if u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
		return ret.PromptTokensDetails != nil &&
			ret.PromptTokensDetails.CachedTokens == int64(u.CacheReadTokens) &&
			ret.PromptTokensDetails.CacheWriteTokens == int64(u.CacheWriteTokens)
	}
	return ret.PromptTokensDetails == nil
}

// go2lean:spec ToResponsesUsageWire
// SpecToResponsesUsageWireTotals: same totals on the Responses wire shape.
func SpecToResponsesUsageWireTotals(u *protocol.TokenUsage, ret *wire.ResponsesUsageWire) bool {
	if u == nil || !nonNegativeUsage(u) {
		return true
	}
	if ret == nil {
		return false
	}
	return ret.InputTokens == int64(u.InputTokens+u.CacheReadTokens) &&
		ret.OutputTokens == int64(u.OutputTokens) &&
		ret.TotalTokens == ret.InputTokens+ret.OutputTokens &&
		ret.InputTokensDetails.CachedTokens == int64(u.CacheReadTokens) &&
		ret.InputTokensDetails.CacheWriteTokens == int64(u.CacheWriteTokens) &&
		ret.OutputTokensDetails.ReasoningTokens == int64(u.ReasoningTokens)
}
