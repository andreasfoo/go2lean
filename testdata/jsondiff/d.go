// Package jsondiff checks go2lean's model of encoding/json against Go:
// every spec here fails on every input, so each search trial is a
// counterexample whose model value (a probe) M3 compares with the real
// one. Run with scripts/json-diff.sh.
package jsondiff

import "encoding/json"

type Inner struct {
	A int    `json:"a,omitempty"`
	B string `json:"b"`
}

type Doc struct {
	Name  string          `json:"name"`
	Count int8            `json:"count,omitempty"`
	U     uint16          `json:"u"`
	Ok    bool            `json:"ok,omitempty"`
	Tags  []string        `json:"tags"`
	Meta  map[string]int  `json:"meta,omitempty"`
	In    *Inner          `json:"in,omitempty"`
	Any   any             `json:"any"`
	Raw   []byte          `json:"raw,omitempty"`
	Inner
	hid
}

// hid is unexported and embedded: Go promotes its exported fields.
type hid struct {
	H string `json:"h,omitempty"`
}

func Encode(d Doc) string {
	b, err := json.Marshal(d)
	if err != nil {
		return "ERR"
	}
	return string(b)
}

// Decode decodes s and encodes the result again.
func Decode(s string) string {
	var d Doc
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return "ERR"
	}
	return Encode(d)
}

// Loose takes any member: raw text, numbers into an int, anything into an
// interface.
type Loose struct {
	R json.RawMessage `json:"r"`
	N int16           `json:"n"`
	A any             `json:"a"`
	P *Loose          `json:"p"`
}

// DecodeLoose decodes s and returns the raw text kept and the result
// encoded again.
func DecodeLoose(s string) string {
	var l Loose
	if err := json.Unmarshal([]byte(s), &l); err != nil {
		return "ERR"
	}
	b, err := json.Marshal(l)
	if err != nil {
		return "ERR " + string(l.R)
	}
	return string(l.R) + "|" + string(b)
}

// EncodeRaw encodes raw text inside a struct.
func EncodeRaw(r json.RawMessage) string {
	b, err := json.Marshal(Loose{R: r})
	if err != nil {
		return "ERR"
	}
	return string(b)
}
