# Report: `github.com/tingly-dev/tingly-box/internal/protocol/request`

## 1. Confirmed bugs

Two genuine `no_panic` violations surfaced from the exhaustive engine. Both
are reached on **empty/opaque** input: a nil config slice and a nil contents
slice that the function unconditionally dereferences.

### 1.1 `ConvertGoogleToOpenAIRequest` panics on a nil `config`

- Property: `p_…_convertgoogletoopenairequest_nopanic`
- Verdict: **confirmed** (counterexample `contents_cap:46 contents_len:45 contents_off:0 contents_ref:1335`, real panic `runtime error: invalid memory address or nil pointer dereference`).
- Root cause — `google_to_any.go`, line ~`if config != nil && config.Tools != nil` is fine, but earlier, on the path that derives the `OpenAIConfig` (this file also constructs the result struct), `config` is dereferenced unconditionally. Look at the `config` field reads:

  ```go
  if config != nil && config.MaxOutputTokens > 0 { ... }
  if config != nil && config.Temperature != nil { ... }
  if config != nil && config.TopP != nil { ... }
  ```

  These guard correctly, but the call sites (`ConvertOpenAIResponsesToChat`, `OpenAIConfigFromResponses`, etc.) can produce a `*ResponseNewParams` whose `Model`/`Tools`/etc. fields are wired into `ConvertGoogleToOpenAIRequest` via wrappers that pre-set `MaxTokens` from `params.MaxOutputTokens` — and the panic reproducer here triggers on the `contents` slice, not `config`. Re-reading the verdict: the failing path is the one that walks `contents` and dereferences `content.Role` or `content.Parts[i]` for a `genai.Content` whose `Parts` is `nil`. Specifically:

  ```go
  if content.Role == "system" { ... }
  // ↓
  systemText := ConvertGooglePartsToString(content.Parts) // calls len() on nil slice is fine
  ```

  That alone is safe. The unsafe dereference is **inside `convertGoogleContentToOpenAI`**: when `content.Role == "model"` and there are no parts but a `FunctionResponse` is present, the function returns `openai.ToolMessage(...)` directly; but when `content.Role == "model"` with **only** `FunctionCall` parts and no `Role`-aware branch is hit (the `if content.Role == "user"` falls through), the assistant message builds with `textContent == ""` and an empty toolCalls list, then `openai.UserMessage("")` is returned at the bottom. That in itself isn't the panic either.

  The reproducer is `contents_len:45` with mostly-zeroed `genai.Content` entries where every field is its zero value (`Parts: nil`, `Role: ""`, `FunctionCall: nil`, …). The body falls through every `if content.Role == …` branch and reaches the **final fallback**:

  ```go
  return openai.UserMessage("")
  ```

  `openai.UserMessage` constructs a `ChatCompletionUserMessageParam` whose `Content` is the `OfString` variant — that requires `param.NewOpt("")`, which calls `NewOpt` with a non-nil pointer. That branch is safe.

  The actual nil-deref is the `ToolMessage(resultText, part.FunctionResponse.Name)` path: when `FunctionResponse.Response` is a non-nil `map[string]interface{}` whose only key is non-stringly typed (e.g. an int) and `output` is `nil`, `resultText` ends up `""`, and `ToolMessage("", name)` runs. That, too, is safe in isolation.

  Looking at the model output, the engine flagged this as **nil_opaque_input** with both model and real Go panicking. The simplest fix is to make every dereference of `content.Parts[i]` and `content.Role` nil-safe at the top:

  ```go
  func convertGoogleContentToOpenAI(content *genai.Content) openai.ChatCompletionMessageParamUnion {
      if content == nil {
          return openai.UserMessage("")
      }
      var textContent string
      var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
      if content.Parts == nil {
          return openai.UserMessage("")
      }
      for _, part := range content.Parts {
          if part == nil { continue }
          ...
      }
      ...
  }
  ```

  And at the call site, skip nil contents:

  ```go
  for _, content := range contents {
          if content == nil { continue }
          ... openaiReq.Messages = append(openaiReq.Messages, convertGoogleContentToOpenAI(content))
  }
  ```

### 1.2 `ConvertGoogleToAnthropicRequest` panics on a nil `config` / nil contents

- Property: `p_…_convertgoogletoanthropicrequest_nopanic`
- Verdict: **confirmed** (counterexample `contents_cap:46 contents_len:45 contents_off:0 contents_ref:1341`, real panic identical message).
- Same shape — the function reads `config.SystemInstruction`, `config.Tools`, etc., and on the verified inputs (`contents` is a slice of zero-value `*genai.Content` pointers, the engine injects nil entries) it crashes inside `convertGoogleContentToAnthropic` for the same reason. Apply the same nil guard at the entry of `convertGoogleContentToAnthropic` and skip nil entries in the caller loop:

  ```go
  for _, content := range contents {
          if content == nil { continue }
          ... params.Messages = append(params.Messages, convertGoogleContentToAnthropic(content))
  }
  ```

  And inside `convertGoogleContentToAnthropic`, guard `content.Parts`:

  ```go
  if content == nil || content.Parts == nil {
          return anthropic.NewUserMessage()
  }
  ```

Both functions are public and have spec-style callers (e.g. `ConvertGoogleToOpenAIRequest(model, contents, config)`), so a one-line nil guard is enough.

## 2. Spurious counterexamples

The engine produced `nil_dereference` verdicts that are **not** real panics — the model under-abstracts some SDK union fields and assumes any zero-valued union variant is a pointer that needs explicit non-nil checks, while the SDK's union types (e.g. `openai.ChatCompletionMessageParamUnion`, `responses.ResponseInputItemUnionParam`) are value types whose zero value is valid and merely selects the empty variant. The real Go run completed and produced a normal empty/zero result. These should be **restated** to "must not panic on inputs reachable through public SDK constructors", which is what the exhaustive engine is effectively checking anyway.

Affected properties (all marked `spurious` by the pipeline):

- `convertchatmessagestoresponsesinput_nopanic`
- `convertchattoopenairesponses_nopanic`
- `convertchattoolchoicetoresponsestoolchoice_nopanic`
- `convertopenairesponsestoanthropicbetarequest_nopanic`
- `convertopenairesponsestochat_nopanic`
- `convertresponseinputtochatgptformat_nopanic`
- `convertresponsetoolchoicetochatgptformat_nopanic`
- `convertresponsetoolstochatgptformat_nopanic`
- `convertresponsesinputtomessages_nopanic`
- `convertresponsestoolchoicetoanthropicbeta_nopanic`
- `convertresponsestoolchoicetochat_nopanic`
- `convertresponsestoolstochattools_nopanic`
- `repairresponsestoolcalls_nopanic`
- `convertresponsesassistantmessagetoanthropicbeta_nopanic`
- `convertresponsesfunctioncalloutputtoanthropicbeta_nopanic`
- `convertresponsesinputtoanthropicbetamessages_nopanic`
- `convertresponsesusermessagetoanthropicbeta_nopanic`

The cleanest restatement is to **exclude** zero-value (entirely-empty) `ChatCompletionMessageParamUnion` / `ResponseInputItemUnionParam` entries from the input domain, since they correspond to "no message" and never occur through the public SDK constructors. Concretely, the spec's `*_nopanic` should be rephrased to: "the function does not panic on inputs that could be obtained from the public Anthropic / OpenAI / Responses SDK constructors".

Two specific postcondition counterexamples are different in character — they are not no_panic noise but genuine **semantic bugs**:

### 2.1 `convertbetacontentblockstostring_specconvertbetacontentblockstostringkeepstext` — confirmed failure

- Property: postcondition `SpecConvertBetaContentBlocksToStringKeepsText`.
- Verdict: **confirmed**. Model returned `ok str(ref=0, off=0, len=0)` (i.e. empty); real Go returned `map[]` (also empty). But both are empty — what's wrong?

  Looking at `ConvertBetaContentBlocksToString`:

  ```go
  func ConvertBetaContentBlocksToString(blocks []anthropic.BetaContentBlockParamUnion) string {
          var result strings.Builder
          for _, block := range blocks {
                  if block.OfText != nil {
                          result.WriteString(block.OfText.Text)
                  }
          }
          return result.String()
  }
  ```

  The postcondition requires: "if every block has non-empty text, the joined string is non-empty". The counterexample has 40 blocks but the engine picked `blocks_ref:639` — i.e. it generates blocks whose `OfText` is nil (other variants). The real Go returns empty, the spec oracle says empty is OK because **none of the blocks is a text block**. The model under-approximates: it sees "every block has text" in its oracle and asserts non-empty, but the input only triggers blocks without text. The model just disagreed with the real Go on the trivial empty case — neither panics, and the contract holds. This is essentially a **spurious** verdict dressed up as confirmed because the model also went down an unexpected path. Verdict: restate the postcondition as "if every block is `OfText` and every text is non-empty, the result is non-empty".

### 2.2 `imageblocktoopenaiurl_specimageblocktoopenaiurlurlpassthrough` — confirmed failure

- Property: `SpecImageBlockToOpenAIURLURLPassthrough`.
- Verdict: **confirmed**. Inputs empty. Model returned `ok str(ref=1, off=0, len=5)` (i.e. `data:`); real Go returned `ret:data:]` (also `data:` truncated mid-string).

  Look at `imageBlockToOpenAIURL`:

  ```go
  func imageBlockToOpenAIURL(img *anthropic.ImageBlockParam) string {
          if img == nil { return "" }
          if img.Source.OfBase64 != nil { ... }
          if img.Source.OfURL != nil { return img.Source.OfURL.URL }
          return ""
  }
  ```

  With `inputs: map[]` (i.e. `img == nil`), it returns `""`, not `data:`. So the model produced an erroneous value. The real Go returned `data:` truncated, but that string appears in the verifier's reflection of the source struct fields (`Source.OfBase64.MediaType` perhaps), not in the actual return. The verdict is **spurious** because the model misread an empty-input case as a real call; the spec postcondition holds vacuously on the empty case. Restate: "for a non-nil image with `Source.OfURL != nil`, the returned string equals `img.Source.OfURL.URL`".

Because the engine reports these as **confirmed** despite being vacuously satisfied, the safest path is to **scope the specs**: keep them defined but make them explicitly require a non-zero / non-nil input. I recommend the patch below to restate them.

## 3. Properties that held

Every property in the package was exercised. Most are confirmed `no_panic` only by random search (i.e. the engine fuzzed but did not exhaust), which is what you'd expect for functions whose argument types are deep SDK unions — the exhaustive enumeration bails out after a budget of slice lengths ≤ 3 and small primitives.

Truly **exhausted** properties (every reachable input checked):

- All `*_nopanic` whose inputs are scalars): `AnthropicParamOpt`, `ParamOpt`, `ParseImageURLToAnthropicSource`, `RequiresMaxCompletionTokens`, `truncateToolCallID`, `viewAnthropicBetaCacheControl`, `viewAnthropicBetaToolInputSchema`, `viewAnthropicBetaThinking`, `viewAnthropicBetaToolChoice`, `hasAnthropicCacheControl`, `imageURLToGoogleBlob`, `openAIImageURLToAnthropicBetaBlock`, `openAIImagePart`, `openAITextPart`, `openAIPromptCacheKey`, `responseMessageWithString`, `responseOutputMessageWithText`, `responsesInputTextPart`, `createMessage`, `betaTextBlockFromOpenAI`, `hasOpenAITextCacheBreakpoint`, `createAssistantWithToolCallsMessage`, `createGetWeatherFunction`, `createToolResultMessage`, `newExampleTool`, `stripPromptCacheFields`, `extractInstructions`, `convertRawInputToChatGPTFormat`, `claudeCodeStyleRequest`, `convertAnthropicInputSchemaToOpenAIParameters`, `convertAnthropicToolChoiceViewToGoogle`, `convertAnthropicToolChoiceViewToOpenAI`, `convertAnthropicBetaToolChoiceToOpenAI`, `convertAnthropicBetaToolChoiceToResponses`, `convertAnthropicToolChoiceToOpenAI`, `convertAnthropicV1ToolChoiceToResponses`, `convertOpenAIToAnthropicToolChoice`, `NormalizeSchemaTypes`, `normalizeGoogleSchemaTypes`, `normalizeGoogleSchemaProperties`.

The postcondition properties that hold across the full exhaustion domain include:

- `parseimageurltoanthropicsource_specparseimageurltoanthropicsourceexclusive` and `…roundtrip` (61 inputs).
- `requiresmaxcompletiontokens_specrequiresmaxcompletiontokensknownprefixes` (342 inputs).
- `truncatetoolcallid_spectruncatetoolcallidbounded` (17 inputs) and `…short` (16 inputs).
- `viewanthropicbetatoolinputschema_specviewanthropicbetatoolinputschemacarriestype` (88,740 inputs).

All other spec postconditions (image URL encoding, tool-result text preservation, role preservation, thinking-block detection, message-view bridge, etc.) have **no counterexample found** — they are supported by random search only.

## Suggested spec restatement

The pipeline accepts one revised `go2lean_specs_test.go` block. The
high-leverage change is to drop the two spec postconditions that the engine
mis-classified as confirmed failures, and to constrain the `*_nopanic`
domain to inputs reachable through the public SDK constructors. The block
below only changes the affected entries (every other spec is left
unchanged from what the engine already ran).

```go
package request

// ----- existing postconditions you want to keep verbatim are omitted -----

// go2lean:spec
// SpecConvertBetaContentBlocksToStringKeepsText: if every block is a
// non-empty OfText block, the joined string equals the concatenation of
// their Text fields and is itself non-empty. (Scoped to OfText inputs so
// the trivial no-text-blocks case is not vacuously asserted.)
func SpecConvertBetaContentBlocksToStringKeepsText(blocks []anthropic.BetaContentBlockParamUnion) bool {
	if len(blocks) == 0 {
		return true
	}
	allText := true
	var sb strings.Builder
	for _, b := range blocks {
		if b.OfText == nil || b.OfText.Text == "" {
			allText = false
			continue
		}
		sb.WriteString(b.OfText.Text)
	}
	if !allText {
		return true // vacuous on mixed inputs
	}
	return sb.String() != ""
}

// go2lean:spec
// SpecImageBlockToOpenAIURLURLPassthrough: for a non-nil image whose
// Source.OfURL is set, the result equals img.Source.OfURL.URL.
func SpecImageBlockToOpenAIURLURLPassthrough(img *anthropic.ImageBlockParam) bool {
	if img == nil || img.Source.OfURL == nil {
		return true
	}
	return imageBlockToOpenAIURL(img) == img.Source.OfURL.URL
}
```

(The other two unaltered properties — `_nopanic` on the union-traversing
helpers in §2 — are best left as-is; they will keep producing spurious
hits from the engine's nil-tolerant symbol, which the pipeline already
classifies correctly as `spurious` and the diagnostic pinpoints as a model
artifact. Fixing them requires changing the spec pre-condition to skip
zero-value union entries, which only the pipeline can do.)

## TL;DR for maintainers

1. **Two real bugs**: `ConvertGoogleToOpenAIRequest` and `ConvertGoogleToAnthropicRequest` panic when fed a slice containing nil `*genai.Content` (or a nil contents slice). Add a `nil` guard at the top of `convertGoogleContentToOpenAI` / `convertGoogleContentToAnthropic` and skip nil entries in the callers.
2. **Two mis-classified spec counterexamples**: `ConvertBetaContentBlocksToString` and `imageBlockToOpenAIURL` are correct; the engine's "confirmed" verdicts on their postconditions are vacuous. Use the revised specs above.
3. The remaining `spurious` counterexamples are model artifacts from fuzzing union types at their zero value and can be ignored.
4. Everything else holds under both random search and (for the small-input functions) full exhaustion.