package contract

import (
	"encoding/json"
	"testing"
)

func TestFlexValue(t *testing.T) {
	for in, want := range map[string]FlexValue{
		`"ptr(ref=1)"`:                        "ptr(ref=1)",
		`["1", "true"]`:                       "[1, true]",
		`{"type": 1, "value": "ptr(ref=10)"}`: `{"type":1,"value":"ptr(ref=10)"}`,
	} {
		var v FlexValue
		if err := json.Unmarshal([]byte(in), &v); err != nil || v != want {
			t.Errorf("%s: got %q, %v; want %q", in, v, err, want)
		}
	}
}
