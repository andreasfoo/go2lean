# Bug hunting on tingly-box, guided by its issue tracker

What was run and what was observed. Every result below is the output of
a go2lean run or of a real `go test` that M3 executed; the PoC of the
confirmed finding is in `docs/poc-issue-1924/`. Interpretation is left to
the reader; the one sentence of ours is marked as such.

## 1. Inputs

- tingly-box issue tracker, label `bug`: 51 closed, 11 open (2026-10-18).
  Open issues about protocol conversion, quoted from their titles:
  #1924 "tool-use ids over 40 characters are truncated on the tool message
  only, so tool calls and results stop pairing"; #1923 "streamed tool_calls
  always use index 0"; #1922, #1921, #1427, #1897.
- Specula's spreadsheet of found bugs (about 270 rows; link in its README).
  We used it only to choose property shapes: bounded ids, preserved counts,
  non-empty in implies non-empty out. The agent guidance file derived from
  both lists is `docs/specs/tingly-box/guidance.md`.

## 2. Properties written by hand

The spec files as installed in the package directories are in
`docs/specs/tingly-box/`.

`internal/protocol/request`, target `ConvertAnthropicBetaToOpenAIRequest`:

- `ToolCallIDsBounded`: every `tool_calls[].id` on an assistant message of
  the converted request has at most 40 characters.
- `ToolMessageIDsBounded`: every tool message's `tool_call_id` has at most
  40 characters.
- `ToolPairsKept`: a tool_result whose `tool_use_id` matches a tool_use in
  the input has, in the output, a tool message whose `tool_call_id`
  equals some assistant `tool_calls[].id`.
- `ConvertBetaToolResultContentKeepsText` (target
  `convertBetaToolResultContent`): a text block with non-empty text gives a
  non-empty result.

`internal/protocol/stream`:

- `truncateToolCallID`: the result has at most 40 characters; it equals the
  input when the input fits, else it is the input's first 40 characters.
- `anthropicStopReasonToIncomplete`: `"max_tokens"` gives `(true, s)` with
  `s != ""`.

## 3. Runs and observations

Options: `-maxlen 48 -budget 120` for the request package, `-maxlen 48`
for the stream package. go2lean at commit `154fddf` or later.

| property | engine output | M3 output |
|---|---|---|
| `ToolCallIDsBounded` | `random: found at trial 168` | `confirmed`; the test's `GO2LEAN_RESULT` line reads `"panicked": false, "spec": false` |
| `ToolMessageIDsBounded` | `none in 2000 trials` | not run (no counterexample) |
| `ToolPairsKept` | `none in 1974 trials (745 out of fuel)` | not run |
| `ConvertBetaToolResultContentKeepsText` | `none in 2000 trials` | not run |
| `truncateToolCallID` bounded | `exhaustive: holds on 16 inputs (recorded as exhausted)` | — |
| `truncateToolCallID` prefix | `exhaustive: holds on 16 inputs` | — |
| `anthropicStopReasonToIncomplete` | `exhaustive: holds on 40 inputs` | — |

The confirmed counterexample (`docs/poc-issue-1924/`): the input request
holds tool_use blocks whose ids have 51, 50, 47 and 43 characters; after
`ConvertAnthropicBetaToOpenAIRequest` the oracle finds an assistant
`tool_calls[].id` longer than 40 characters.

In the source at the tested commit (`5f83f04`):
`internal/protocol/request/anthropic_view.go` lines 94, 416 and 851 assign
`block.OfToolUse.ID` to the call-side id without change; line 510 assigns
`truncateToolCallID(block.ToolUseID)` to the result-side id.

Our judgement, the only one in this document: this matches the symptom
issue #1924 reports.

The templated `no_panic` properties of `internal/protocol/stream` produced
13 counterexamples. M3 outputs: 7 `confirmed`, 4 `spurious`
(`no_panic_in_real_run`), 2 `inconclusive` (`target_exited`). Of the 7
confirmed, 3 carry the diagnosis `nil_opaque_input` (an interface, map or
func input was nil in the test); 3 are methods of the package's test-only
types `fakeAnthropicDecoder`, `fakeChatDecoder`, `fakeResponsesDecoder`
(`index out of range` with an index taken from a field of the receiver);
1 is `chatToResponsesConverter.emitCompletionEvents` with a receiver whose
index field the test set to -2 (`index out of range [-2]`).

## 3b. The agent with the guidance file

`go2lean agent -guidance docs/specs/tingly-box/guidance.md -maxlen 48 -budget 60`
on `internal/protocol/request` (MiniMax-M3, thinking off). Artefacts:
`docs/specs/tingly-box/agent-request/` (prompts, responses, report).

- Proposal round: 47 specs, all modelled.
- Two postconditions were refuted and M3 reported `confirmed`:
  - `SpecImageBlockToOpenAIURLURLPassthrough` on `imageBlockToOpenAIURL`:
    the input's `Source.OfURL.URL` is the string `"data:"`; the function
    returned `"data:"`; the spec returns false for any result starting with
    `data:`.
  - `SpecConvertBetaContentBlocksToStringKeepsText` on
    `ConvertBetaContentBlocksToString`: the input's top-level text block
    has `Text == ""`; the function returned `""`; the spec returns false
    when a text block is present and the result is empty.
- In the report round the agent replaced the spec file with one containing
  no spec functions (its comment says the parameter types fall outside the
  modelled subset, which the proposal round had just contradicted).
- The `no_panic` properties of the package: 2 `confirmed`
  (`ConvertGoogleToAnthropicRequest`, `ConvertGoogleToOpenAIRequest`, both
  nil dereferences with `nil_opaque_input`), 15 `spurious`.

## 4. Changes made to go2lean during the hunt

- Unions of the OpenAI and Anthropic SDKs (structs whose pointer fields are
  all `Of<Variant>`) are generated one variant at a time; before, every
  variant was non-nil and the request type had 4718 leaf generators, 15
  trials per 10 s.
- The literal pool is mined from the target's whole call tree.
- A slice generated at depth zero is empty; before, a union holding a slice
  of itself recursed until the stack overflowed.
- `GO2LEAN_TRACE=1` prints every input the random engine draws.
- `-guidance file` appends text to the agent's proposal prompt.
