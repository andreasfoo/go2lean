Bug classes seen in this repository's issue tracker (prefer specs that would have caught them):

- Protocol conversion between Anthropic, OpenAI Chat, OpenAI Responses and Google request/response formats:
  - tool_call / tool_use ids must be preserved consistently on both the call and the result side; OpenAI limits ids to 40 characters, so truncation must apply to both sides or neither (#1924);
  - several parallel tool calls must keep distinct indexes / ids; converters must not collapse them (#1923);
  - tool result content given as an array of text blocks must not be dropped (#1427);
  - system / developer messages in the middle of a conversation must not be dropped (#1921);
  - thinking / reasoning effort and budgets must map to a non-trivial effort, not silently to "low" or nothing (#1897);
  - stop/finish reasons must map consistently in both directions.
- Content normalisation: text that goes in must come out (no empty results for non-empty inputs); counts of messages, blocks and tool calls preserved unless the function documents otherwise.
- Routing / load balancing: tier selection must pick the highest tier when it is available (#1215); exhausted services must not be selected (#1011).
- Config parsing: switching modes must not fill the wrong endpoint (#1273).

Specs that compare lengths and counts (len(input blocks) vs len(output blocks), ids bounded by 40, non-empty in implies non-empty out) are the most valuable: they stay in the modelled subset and reproduce in a real test.
