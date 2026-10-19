package sse

// Hand-written go2lean specs for the SSE line helper.

// go2lean:spec ParseSSEDataPayload
// SpecParseSSEDataPayloadPrefix: a line is accepted exactly when it starts
// with "data:", and the payload is the rest of the line after "data:" or
// "data: ".
func SpecParseSSEDataPayloadPrefix(line string, ret0 string, ret1 bool) bool {
	isData := len(line) >= 5 && line[:5] == "data:"
	if ret1 != isData {
		return false
	}
	if !ret1 {
		return ret0 == ""
	}
	return "data: "+ret0 == line || "data:"+ret0 == line
}
