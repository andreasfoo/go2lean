// Package jsonc is a ground-truth package for encoding/json of structs:
// field tags, omitempty, nested values, maps, pointers and Go's string
// escaping.
package jsonc

import "encoding/json"

// Msg is a chat message as a gateway stores it.
type Msg struct {
	Role    string            `json:"role"`
	Content string            `json:"content,omitempty"`
	N       int               `json:"n"`
	Tags    []string          `json:"tags,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
	Reply   *Msg              `json:"reply,omitempty"`
	secret  string
}

// RoundTrip encodes and decodes a message.
func RoundTrip(m Msg) Msg {
	b, err := json.Marshal(m)
	if err != nil {
		return Msg{}
	}
	var out Msg
	if err := json.Unmarshal(b, &out); err != nil {
		return Msg{}
	}
	return out
}

// Limit is a setting whose zero value means "no tokens".
type Limit struct {
	Max int `json:"max,omitempty"`
}

// Reload stores a limit and loads it over the defaults. Bug: a limit of 0
// is omitted when encoding, so loading keeps the default of 100.
func Reload(l Limit) Limit {
	b, _ := json.Marshal(l)
	out := Limit{Max: 100}
	_ = json.Unmarshal(b, &out)
	return out
}

// Quote returns s as the body of a JSON string literal. Bug: json.Marshal
// escapes <, > and & (HTML-safe), so the body is not s even when s needs no
// JSON escaping.
func Quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
