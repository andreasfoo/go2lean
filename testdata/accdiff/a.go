// Package accdiff checks go2lean's model of anthropic-sdk-go's
// Message.Accumulate (translated from the SDK's source) against Go: every
// spec here fails on every input, so each search trial is a counterexample
// whose model value (a probe) M3 compares with the real one. Run with
// DEPS=1 PKG=testdata/accdiff scripts/json-diff.sh.
package accdiff

import (
	"encoding/json"
	"strconv"

	"github.com/anthropics/anthropic-sdk-go"
)

// Trace accumulates the events and renders, per event, its type, index,
// whether Accumulate failed, and the number of blocks after it, then the
// blocks (type, text, input) and the stop reason.
func Trace(evs []anthropic.MessageStreamEventUnion) string {
	var m anthropic.Message
	out := ""
	for _, e := range evs {
		r := "ok"
		if err := m.Accumulate(e); err != nil {
			r = "err"
		}
		out += e.Type + "@" + strconv.FormatInt(e.Index, 10) + ":" + r + ":" + strconv.Itoa(len(m.Content)) + ";"
	}
	for _, b := range m.Content {
		out += "|" + b.Type + "/" + b.Text + "/" + string(b.Input) + "/" + b.Thinking + "/" + b.Signature
	}
	return out + "|" + string(m.StopReason)
}

// stream decodes one of a corpus of streams, event by event.
func stream(i int) []anthropic.MessageStreamEventUnion {
	if i < 0 {
		i = -i
	}
	var lines []string
	switch i % 12 {
	case 0:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"Hel\"}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"lo\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"cache_read_input_tokens\":2}}", "{\"type\":\"message_stop\"}"}
	case 1:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"ok\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\": \"content_block_start\", \"index\": 1, \"content_block\": {\"type\": \"tool_use\", \"id\": \"toolu_1\", \"name\": \"get\", \"input\": {}}}", "{\"type\": \"content_block_delta\", \"index\": 1, \"delta\": {\"type\": \"input_json_delta\", \"partial_json\": \"{\\\"city\\\":\"}}", "{\"type\": \"content_block_delta\", \"index\": 1, \"delta\": {\"type\": \"input_json_delta\", \"partial_json\": \"\\\"Paris\\\"}\"}}", "{\"type\": \"content_block_stop\", \"index\": 1}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":9}}", "{\"type\":\"message_stop\"}"}
	case 2:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"tool_use\", \"id\": \"toolu_2\", \"name\": \"noargs\", \"input\": {}}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":9}}", "{\"type\":\"message_stop\"}"}
	case 3:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"thinking\", \"thinking\": \"\", \"signature\": \"\"}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"thinking_delta\", \"thinking\": \"hmm\"}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"signature_delta\", \"signature\": \"sig\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\": \"content_block_start\", \"index\": 1, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}", "{\"type\": \"content_block_delta\", \"index\": 1, \"delta\": {\"type\": \"text_delta\", \"text\": \"x\"}}", "{\"type\": \"content_block_stop\", \"index\": 1}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"cache_read_input_tokens\":2}}", "{\"type\":\"message_stop\"}"}
	case 4:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 1, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}"}
	case 5:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"x\"}}"}
	case 6:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}", "{\"type\": \"content_block_start\", \"index\": 1, \"content_block\": {\"type\": \"tool_use\", \"id\": \"toolu_3\", \"name\": \"a\", \"input\": {}}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"interleaved\"}}", "{\"type\": \"content_block_delta\", \"index\": 1, \"delta\": {\"type\": \"input_json_delta\", \"partial_json\": \"{}\"}}", "{\"type\": \"content_block_stop\", \"index\": 1}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":9}}", "{\"type\":\"message_stop\"}"}
	case 7:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"cache_read_input_tokens\":2}}", "{\"type\":\"message_stop\"}"}
	case 8:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"tool_use\", \"id\": \"toolu_4\", \"name\": \"b\", \"input\": {}}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"input_json_delta\", \"partial_json\": \"\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\":\"message_stop\"}"}
	case 9:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"text\", \"text\": \"pre\"}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"fix\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\": \"content_block_start\", \"index\": 1, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}", "{\"type\": \"content_block_stop\", \"index\": 1}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"cache_read_input_tokens\":2}}", "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":9}}", "{\"type\":\"message_stop\"}"}
	case 10:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"tool_use\", \"id\": \"toolu_5\", \"name\": \"c\", \"input\": {}}}", "{\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"input_json_delta\", \"partial_json\": \"{\\\"a\\\":1\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\":\"message_stop\"}"}
	case 11:
		lines = []string{"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}", "{\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"redacted_thinking\", \"data\": \"abc\"}}", "{\"type\": \"content_block_stop\", \"index\": 0}", "{\"type\":\"message_stop\"}"}
	}
	var evs []anthropic.MessageStreamEventUnion
	for _, l := range lines {
		var e anthropic.MessageStreamEventUnion
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			continue
		}
		evs = append(evs, e)
	}
	return evs
}

// Corpus traces one of the corpus streams.
func Corpus(i int) string { return Trace(stream(i)) }

// C0 to C11 trace one corpus stream each, so every stream is compared
// whatever the search's seeds.
func C0() string  { return Corpus(0) }
func C1() string  { return Corpus(1) }
func C2() string  { return Corpus(2) }
func C3() string  { return Corpus(3) }
func C4() string  { return Corpus(4) }
func C5() string  { return Corpus(5) }
func C6() string  { return Corpus(6) }
func C7() string  { return Corpus(7) }
func C8() string  { return Corpus(8) }
func C9() string  { return Corpus(9) }
func C10() string { return Corpus(10) }
func C11() string { return Corpus(11) }
