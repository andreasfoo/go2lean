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

## 5. Second round: properties beyond issue #1924

go2lean at `f6f8ed1` to `778525f` (models linked across packages, calls
without results followed, interface values boxed, nil-ness of func, map,
channel and interface values tracked). Spec files: `docs/specs/tingly-box/`.
Options: `-maxlen 48`, budget 60 to 90 s per property.

| property (target) | engine output | M3 output |
|---|---|---|
| `KeepsSystemMessages` (`ConvertAnthropicBetaToOpenAIRequest`) | found at trial 994 | `confirmed` |
| `KeepsSystemMessages` (`convertV1MessagesToResponsesInput`) | found at trial 1 | `confirmed`; the real result is an empty input list |
| `ParallelToolCallIndexes` (`anthropicToOpenAIConverter.processEvent`) | found at trial 0 | `confirmed` |
| `MaxTokensIsLength` (`anthropicToOpenAIConverter.processEvent`) | found at trial 0 | `confirmed` |
| `ToolCallIDsBounded` (`ConvertAnthropicBetaToOpenAIRequest`) | found at trial 882 | `confirmed` |
| `KeepsEveryMessage` (`ConvertAnthropicBetaToOpenAIRequest`) | found at trial 49 | `confirmed` |
| `ToolPairsKept` (`ConvertAnthropicBetaToOpenAIRequest`) | found at trial 485 | `confirmed` |
| `WellFormedToolPairs` (`ConvertAnthropicBetaToOpenAIRequest`) | none in 2000 trials | not run |
| `ToolMessageIDsBounded`, `ConvertBetaToolResultContentKeepsText` | none in 2000 trials | not run |
| 7 usage specs (`FromOpenAIChatCompletion`, `FromAnthropicBetaMessage`, `ChatUsage`, `ToChatUsageWire`, `ToChatStreamUsageWire`, `ToResponsesUsageWire`) | none in 2000 trials each | not run |
| 3 response specs (`nonstream.ConvertAnthropicBetaToOpenAIChat`: tool call count, text, finish reason) | none in about 460 trials each (budget) | not run |
| `ParseSSEDataPayload` prefix, `truncateToolCallID` (2), `anthropicStopReasonToIncomplete` | exhaustive: holds (48, 16, 16, 40 inputs) | certificates |

Inputs and source lines, as observed:

- `KeepsSystemMessages` on the Chat path: 22 input messages; message 13
  has role `"system"` and two non-empty text blocks; the request's
  top-level `system` field is empty. The converted request has no system
  or developer message. The message loop at
  `internal/protocol/request/anthropic_view.go:287-296` has cases for the
  user and assistant roles only.
- `KeepsSystemMessages` on the Responses path: `anthropic_v1_to_responses.go:98-106`
  appends input items for roles `"user"` and `"assistant"` only.
- `ParallelToolCallIndexes`: the spec feeds two `content_block_start`
  events of type `tool_use` (ids `toolu_first`, `toolu_second`) to the
  converter; both emitted chunks carry `tool_calls[0].index == 0`
  (`internal/protocol/stream/anthropic_to_openai_converter.go:152`; the
  argument deltas at `:180`).
- `MaxTokensIsLength`: the spec feeds a `message_delta` with stop reason
  `max_tokens`, then `message_stop`; the last chunk's `finish_reason` is
  not `"length"`. `processEvent` sets `"stop"` or `"tool_calls"` only
  (`:217-219`).
- `KeepsEveryMessage` and `ToolPairsKept`: the counterexamples hold
  messages whose roles are strings other than `user` and `assistant`.
- In the first run of this round, before cross-package linking, five of
  the usage specs were refuted in the model; M3 reported three
  `spurious` and two `inconclusive` (harness compile errors). After
  linking `ai.NewTokenUsageFull` and `(*ai.TokenUsage).PromptTotalTokens`
  into the model, none of the seven was refuted.

Written, not yet run: two request round-trip specs that compare a client
talking to an upstream of its own protocol with one going through the
conversion (`SpecConvertAnthropicBetaToOpenAIRequestRoundTrip`,
`SpecConvertOpenAIToAnthropicRequestRoundTrip`), with the SDK constructors
linked by `compile -deps`.

## 6. Changes made to go2lean in the second round

- Calls into sibling packages of the same compile are linked
  (`xcall`); `compile -deps` also models the dependency functions reached
  by static calls (bounded), e.g. the SDK constructors.
- Calls without results are followed; a stuck callee falls back to a
  havoc result instead of making the run moot.
- Interface values the program builds are boxed with their dynamic type;
  type assertions unbox them (new panic kind `type_assertion`).
- Nil-ness of func, map, channel and interface values: nil literals, zero
  values and opaque inputs M3 passes as nil are nil; values the program
  builds are non-nil; only unmodelled call results are unknown.
- M3: compile errors without a column are recognised; zero-valued
  fields are left out of literals; a non-nil pointer to an opaque object
  is passed as `new(T)`.

## 7. Third round: equivalence of direct and converted paths

Specs (in `docs/specs/tingly-box/`), go2lean `ad4d134` to `6871c36` with
`compile -deps`, `-maxlen 6`, budget 180 s:

- Request round trips: an Anthropic request converted to OpenAI Chat and
  back, and an OpenAI Chat request converted to Anthropic and back, keep
  the system text, each role's text, the tool calls (id and name) and the
  tool results (id and text). Preconditions: roles user and assistant
  (plus system and tool for OpenAI), tool ids non-empty and at most 40
  characters.
- Streaming vs non-streaming: one Anthropic message replayed as an event
  stream through the stream converter, and converted by
  `nonstream.ConvertAnthropicBetaToOpenAIChat`; text, reasoning text,
  tool calls and finish reason compared. Preconditions: thinking blocks
  carry their text in `Thinking`; the stop reason is `tool_use` exactly
  when a tool_use block is present; `max_tokens` excluded.

| property | engine output | M3 |
|---|---|---|
| request round trip, Anthropic -> Chat -> Anthropic | none in 2000 trials; 950 got past the guards | not run |
| request round trip, Chat -> Anthropic -> Chat | none in 2000 trials; 1193 got past the guards | not run |
| stream vs non-stream: text | none in 2000 trials; 2000 checked | not run |
| stream vs non-stream: tool calls | none in 2000 trials; 2000 checked | not run |
| stream vs non-stream: finish reason | none in 2000 trials; 1651 checked | not run |
| stream vs non-stream: reasoning text | found at trial 1335 | `confirmed` |

The confirmed case and a second check on a decoded API response are in
`docs/poc-tingly-round3/`: for a `thinking` block the non-streaming
converter returns an empty `reasoning_content`, the streaming converter
the thinking text. A search of the tracker for `reasoning_content`
(2026-10-19) lists #1863 (the Chat -> Anthropic direction) and none for
this direction.

Earlier runs of the same specs, before the model changes listed below,
gave refutations M3 judged `spurious` (the model lost the system text in
the generic `openai.SystemMessage`, which was then unmodelled) and rounds
in which every trial past the guards was moot (`time.Now`, a
package-level map, `param.IsOmitted`). Without the precondition on the
stop reason, the finish-reason spec was refuted on messages whose stop
reason disagrees with their blocks (`tool_use` without a tool_use block,
`pause_turn` with one); M3 confirmed those too.

Changes to go2lean in this round: the exit report (`spec checked N,
vacuous M`), named-type string constants in the generators, the havoc
agreement rule, field-wise havoc of struct results, assumption markers
and their names in the report, `-deps` following generic instances.
