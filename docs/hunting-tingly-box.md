# Bug hunting on tingly-box, guided by its issue tracker

How the properties were chosen, what they proved, and what they found.
Everything here was reproduced by M3 with a real `go test`; the PoC for the
confirmed finding is in `docs/poc-issue-1924/`.

## 1. Where the properties come from

The tracker (62 bug issues, 11 open) clusters in protocol conversion
between the Anthropic, OpenAI Chat, OpenAI Responses and Google formats:

| issue | invariant it states |
|---|---|
| #1924 (open) | tool-call ids are truncated to OpenAI's 40 characters on the tool-result side only, so calls and results stop pairing |
| #1923 (open) | parallel tool calls in a stream must keep distinct indexes |
| #1427 (closed) | a tool result given as an array of text blocks must not be dropped |
| #1921 (open) | mid-conversation system messages must survive the OpenAI paths |
| #1897 (closed) | thinking budgets must map to a real effort, not silently to "low" |

Specula's list of found bugs (about 270 rows) is mostly consensus and
memory-safety work in distributed systems, but roughly half of the
entries are single-function logic errors: truncation and buffer sizing,
inverted conditions, counter asymmetry, validation gaps, off-by-one. Those
are the class go2lean's properties target, so the guidance file for the
agent (`docs/specs/tingly-box/guidance.md`) lists exactly these shapes:
ids bounded by 40 on both sides, counts preserved, non-empty in implies
non-empty out.

## 2. Properties written by hand

`docs/specs/tingly-box/` holds the spec files as installed in the package
directories (`go2lean_specs_test.go`).

`internal/protocol/request`, target `ConvertAnthropicBetaToOpenAIRequest`:

- `ToolCallIDsBounded`: every tool_call id on an assistant message of the
  converted request is at most 40 characters.
- `ToolMessageIDsBounded`: every tool message's tool_call_id is at most 40
  characters.
- `ToolPairsKept`: a tool_result paired with a tool_use in the input is
  still paired in the output.
- `ConvertBetaToolResultContentKeepsText`: a text block with content makes
  the converted tool result non-empty.

`internal/protocol/stream`:

- `truncateToolCallID`: the result is at most 40 characters, and a prefix
  of the input, kept whole when it fits.
- `anthropicStopReasonToIncomplete`: `max_tokens` maps to an incomplete
  stop with a non-empty reason.

## 3. Results

Run with `-maxlen 48` (ids longer than 40 characters must be drawable) and
`-budget 120` on the wide request type.

| property | engine result | M3 |
|---|---|---|
| `ToolCallIDsBounded` | refuted at trial 168 | **confirmed**: the converted request carries assistant tool_call ids of 43, 47, 50 and 51 characters |
| `ToolMessageIDsBounded` | no counterexample in 2000 trials | — |
| `ToolPairsKept` | no counterexample (745 of 1974 runs out of fuel) | — |
| `ConvertBetaToolResultContentKeepsText` | no counterexample in 2000 trials | — |
| `truncateToolCallID` bounded, prefix | certified: holds on the whole small domain (16 inputs each) | — |
| `anthropicStopReasonToIncomplete` | certified (40 inputs) | — |

The confirmed finding is the root cause of issue #1924: the call side
copies `block.OfToolUse.ID` verbatim (`anthropic_view.go` lines 94, 416,
851) while the result side goes through `truncateToolCallID`
(`anthropic_view.go:510`), so for an id longer than 40 characters the tool
message's `tool_call_id` no longer equals the assistant's `tool_calls[].id`.
The PoC test builds a request with such an id, converts it, and prints the
oracle's verdict (`"spec": false`).

The templated `no_panic` properties of `internal/protocol/stream` found
thirteen counterexamples; M3 confirmed seven, of which three are nil
dereferences of an interface the test can only pass as nil
(`nil_opaque_input`), three are the package's own fake decoders indexing
past their event list, and one (`chatToResponsesConverter.emitCompletionEvents`)
indexes with a state field the test set to -2: a state the converter never
builds itself. Four were spurious (havoc values), two ended the test
process. None of these seven is a finding to report upstream.

## 4. What the hunt changed in go2lean

- A union in the OpenAI and Anthropic SDKs is a struct whose pointer
  fields are all `Of<Variant>`; with pointers non-nil by default every
  variant was live at once and a request type had 4718 leaf generators.
  Unions now get one variant at a time.
- The literal pool is mined from the whole call tree, so `"assistant"` in a
  callee is reachable from the caller's inputs.
- A slice at depth zero is empty; before, a union holding a slice of itself
  recursed until the stack overflowed.
- `GO2LEAN_TRACE=1` prints every input the random engine draws.
- `-guidance file` adds issue-derived bug classes to the agent's prompt.
