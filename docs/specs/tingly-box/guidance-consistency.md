Task: streaming / non-streaming consistency specs for package internal/protocol/stream.

A client that asks tingly-box for a stream must receive what a client that asks for a
non-streamed response receives for the same upstream answer. Write specs that replay one
upstream answer through a stream converter and compare the result with the non-streaming
converter of package nonstream (imported as "github.com/tingly-dev/tingly-box/internal/protocol/nonstream";
calling its exported functions IS allowed in these specs).

Write ONLY into your own spec file. The package already has a hand-written spec file with
these helpers, which you may call and must not redefine:
- replayMessage(bm anthropic.BetaMessage) replayed            (Anthropic -> Chat stream replay)
- nonStreamed(bm anthropic.BetaMessage) (text, reasoning, finish string, calls []string, ok bool)
- replayToResponses(bm anthropic.BetaMessage) (wire.ResponsesWireResponse, bool)
- responseSummary(r wire.ResponsesWireResponse) (status, reason, text string, calls []string)
- apiShaped(bm anthropic.BetaMessage) bool
Read them in the package source (go2lean_specs_test.go) as templates: each spec ignores the
random converter it is given, builds a fresh converter with the package's constructor, feeds it
a whole event sequence built from one random upstream answer, collects the converter's
`pending` queue (type-assert each element to the concrete event or chunk type), and compares
with the non-streaming conversion of the same answer.

Pairs to cover (one spec per compared field: text, tool/function calls as "id/name" in
order, finish or stop reason, reasoning text where both sides have it):

1. Responses -> Chat. Marker: go2lean:spec responsesToChatConverter.processEvent;
   signature (c *responsesToChatConverter, evt *responses.ResponseStreamEventUnion) bool.
   Upstream answer: evt.Response (a responses.Response). Stream: c2 := newResponsesToChatConverter(nil, "m", true),
   feed events built from evt.Response.Output: "response.created", then per output item
   "response.output_item.added" (Item, OutputIndex), "response.output_text.delta" (Delta) for
   message text, "response.function_call_arguments.delta" for calls, "response.output_item.done",
   finally "response.completed" with Response = evt.Response. Pending elements are
   wire.ChatStreamChunk. Non-stream: nonstream.ConvertResponsesToOpenAIChat(&evt.Response, "m").

2. Responses -> Anthropic. Marker: go2lean:spec responsesToAnthropicConverter.processEvent;
   signature (r *responsesToAnthropicConverter, currentEvent responses.ResponseStreamEventUnion) bool.
   Stream: r2 := newResponsesToAnthropicConverter(context.Background(), nil, "m"), same event
   sequence as in 1. Pending elements are the converter's emitted Anthropic events (see
   emitAnthropic in openai_responses_to_anthropic_converter.go for their type). Non-stream:
   nonstream.HandleResponsesToAnthropicBeta(&currentEvent.Response, "m") (a BetaMessage):
   compare text blocks, tool_use blocks (id/name) and stop_reason.

3. Chat -> Responses. Marker: go2lean:spec chatToResponsesConverter.processChunk;
   signature (c *chatToResponsesConverter, chunk *openai.ChatCompletionChunk) bool.
   Upstream answer: build an openai.ChatCompletion from the random chunk's first choice
   (message content = Delta.Content, tool calls = Delta.ToolCalls with their ids and names,
   finish_reason = FinishReason). Stream: c2 := NewChatToResponsesConverter(nil, "m"), feed
   the chunk, then a final chunk with the finish reason. Pending elements are wire.ResponsesEvent;
   take the Response of the last wire.ResponsesCompletedEvent or wire.ResponsesIncompleteEvent and
   compare with nonstream.ConvertChatToResponsesWire(&completion, "m", "m") using responseSummary.

4. Chat -> Anthropic. Marker: go2lean:spec openAIToAnthropicConverter.processChunk;
   signature (c *openAIToAnthropicConverter, chunk *openai.ChatCompletionChunk) bool.
   Same upstream answer as in 3. Stream: newOpenAIToAnthropicConverter(nil, "m", nil, nil, nil).
   Non-stream: nonstream.HandleOpenAIChatToAnthropic(&completion, "m").

Preconditions: keep answers shaped as the APIs send them (return true otherwise): a
Responses answer whose output holds message and function_call items only; a finish reason
of "tool_calls" exactly when tool calls are present; ids non-empty. Name every spec
Spec<Target><What>Matches. Plain ASCII, no closures, no fmt.
