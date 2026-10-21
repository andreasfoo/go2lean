// Package jsondiff checks go2lean's model of encoding/json against Go:
// every spec here fails on every input, so each search trial is a
// counterexample whose model value (a probe) M3 compares with the real
// one. Run with scripts/json-diff.sh.
package jsondiff

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

type Inner struct {
	A int    `json:"a,omitempty"`
	B string `json:"b"`
}

type Doc struct {
	Name  string         `json:"name"`
	Count int8           `json:"count,omitempty"`
	U     uint16         `json:"u"`
	Ok    bool           `json:"ok,omitempty"`
	Tags  []string       `json:"tags"`
	Meta  map[string]int `json:"meta,omitempty"`
	In    *Inner         `json:"in,omitempty"`
	Any   any            `json:"any"`
	Raw   []byte         `json:"raw,omitempty"`
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

// Encode64 encodes in the four encodings of package base64.
func Encode64(s string) string {
	b := []byte(s)
	return base64.StdEncoding.EncodeToString(b) + "|" + base64.URLEncoding.EncodeToString(b) + "|" +
		base64.RawStdEncoding.EncodeToString(b) + "|" + base64.RawURLEncoding.EncodeToString(b)
}

// DecodeURL64 decodes URL-safe base64, padded and raw.
func DecodeURL64(s string) string {
	out := ""
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		out += string(b)
	} else {
		out += "ERR"
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		out += "|" + string(b)
	} else {
		out += "|ERR"
	}
	return out
}

// Decode64 decodes standard base64.
func Decode64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "ERR"
	}
	return string(b)
}

// DecodeSchema decodes an SDK param type (apijson, which coerces or skips
// a member of the wrong JSON type instead of failing).
func DecodeSchema(s string) string {
	var p anthropic.BetaToolInputSchemaParam
	err := json.Unmarshal([]byte(s), &p)
	out := string(p.Type) + "|"
	for _, r := range p.Required {
		out += r + ","
	}
	if _, ok := p.ExtraFields["x"]; ok {
		out += "|x"
	}
	if err != nil {
		out += "|ERR"
	}
	return out
}

// DecodeCitations decodes an SDK response type (apijson): a bool member
// of another JSON type is coerced. (The member's status, JSON.Enabled,
// needs the SDK's respjson package, which json-diff does not compile.)
func DecodeCitations(s string) string {
	var c anthropic.CitationsConfig
	err := json.Unmarshal([]byte(s), &c)
	out := "f"
	if c.Enabled {
		out = "t"
	}
	if err != nil {
		out += "E"
	}
	return out
}

// EncodeStream encodes with json.Encoders into strings.Builders: a string
// holding HTML characters, U+2028 and a literal `<`, and a Doc, with
// HTML escaping off, then a string with the default escaping.
func EncodeStream(s string, d Doc) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode("<a&b> \\u003c" + s)
	enc.Encode(d)
	var c strings.Builder
	json.NewEncoder(&c).Encode("<x>&" + s)
	return b.String() + c.String()
}

// ParseFloat parses a decimal built from a and b with one of a few
// suffixes (exponents, overflow, syntax errors) and encodes the value.
func ParseFloat(a int16, b uint16, k uint8) string {
	sfx := []string{"", "e3", "E-2", "e+400", "x", "e", "0000000000000000000001", "e-330"}[k%8]
	f, err := strconv.ParseFloat(strconv.Itoa(int(a))+"."+strconv.Itoa(int(b))+sfx, 64)
	if err != nil {
		return "err"
	}
	out, _ := json.Marshal(f)
	return string(out)
}
