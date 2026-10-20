// Package inputs is a ground-truth package for map and interface inputs:
// the bugs need a map holding a particular entry, or an interface holding
// a value of a particular dynamic type.
package inputs

import "encoding/json"

// Weight returns the weight of name. Bug: a weight of 0 stored for the
// name divides by zero.
func Weight(weights map[string]int, name string) int {
	w, ok := weights[name]
	if !ok {
		return 1
	}
	return 100 / w
}

// Label renders a decoded JSON value. Bug: an object whose "role" is the
// string "system" indexes past an empty slice.
func Label(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case map[string]interface{}:
		if r, ok := x["role"].(string); ok && r == "system" {
			var parts []string
			return parts[0]
		}
		return "object"
	}
	return ""
}

// Count returns how many values of extras are non-empty strings.
func Count(extras map[string]any) int {
	n := 0
	for _, v := range extras {
		if s, ok := v.(string); ok && s != "" {
			n++
		}
	}
	return n
}

// Role decodes a chat message and returns its role. Bug: a "tool"
// message reads the second element of an empty slice.
func Role(raw string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	r, _ := m["role"].(string)
	if r == "tool" {
		var ids []string
		return ids[1]
	}
	return r
}

// Text decodes a JSON string. It never panics.
func Text(raw []byte) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "?"
	}
	return s
}
