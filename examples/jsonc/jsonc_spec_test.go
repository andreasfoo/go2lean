package jsonc

// go2lean:spec RoundTrip
// SpecRoundTripKeeps: the members survive a round trip (an unexported field
// does not, by design).
func SpecRoundTripKeeps(m Msg, ret Msg) bool {
	if ret.Role != m.Role || ret.Content != m.Content || ret.N != m.N || len(ret.Tags) != len(m.Tags) || len(ret.Meta) != len(m.Meta) {
		return false
	}
	for i := range m.Tags {
		if ret.Tags[i] != m.Tags[i] {
			return false
		}
	}
	return (ret.Reply == nil) == (m.Reply == nil)
}

// go2lean:spec Reload
// SpecReloadKeeps: what was stored is what is loaded.
func SpecReloadKeeps(l Limit, ret Limit) bool {
	return ret.Max == l.Max
}

// go2lean:spec Quote
// SpecQuotePlain: a string without quotes, backslashes or control
// characters is its own JSON body.
func SpecQuotePlain(s string, ret string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' || s[i] < 0x20 || s[i] >= 0x80 {
			return true
		}
	}
	return ret == s
}
