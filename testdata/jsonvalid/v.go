// Package jsonvalid checks go2lean's model of encoding/json.Valid against
// Go: every spec here fails on every input, so each search trial is a
// counterexample whose model value (a probe) M3 compares with the real one.
// Run with PKG=testdata/jsonvalid scripts/json-diff.sh.
package jsonvalid

import "encoding/json"

func corpus(i int) string {
	if i < 0 {
		i = -i
	}
	switch i % 58 {
	case 0:
		return ""
	case 1:
		return " "
	case 2:
		return "0"
	case 3:
		return "-0"
	case 4:
		return "01"
	case 5:
		return "1."
	case 6:
		return ".5"
	case 7:
		return "1e5"
	case 8:
		return "1E+5"
	case 9:
		return "1e"
	case 10:
		return "-"
	case 11:
		return "--1"
	case 12:
		return "+1"
	case 13:
		return "1.5e-3"
	case 14:
		return "true"
	case 15:
		return "tru"
	case 16:
		return "True"
	case 17:
		return "null"
	case 18:
		return "nul"
	case 19:
		return "\"a\""
	case 20:
		return "\"a"
	case 21:
		return "\"\\u00e9\""
	case 22:
		return "\"\\u12\""
	case 23:
		return "\"\\x\""
	case 24:
		return "\"\\/\""
	case 25:
		return "\"a\\tb\""
	case 26:
		return "[]"
	case 27:
		return "[1,]"
	case 28:
		return "[,1]"
	case 29:
		return "[1 2]"
	case 30:
		return "{}"
	case 31:
		return "{\"a\":1}"
	case 32:
		return "{\"a\":1,}"
	case 33:
		return "{\"a\"}"
	case 34:
		return "{a:1}"
	case 35:
		return "{\"a\":1,\"a\":2}"
	case 36:
		return " {\"a\" : [ 1 , 2 ] } "
	case 37:
		return "[1]x"
	case 38:
		return "1 2"
	case 39:
		return "{\"a\":{\"b\":[{\"c\":null}]}}"
	case 40:
		return "\"\\ud800\""
	case 41:
		return "[[[[[[[[1]]]]]]]]"
	case 42:
		return "1e999"
	case 43:
		return "9999999999999999999999"
	case 44:
		return "\"tab\there\""
	case 45:
		return "'a'"
	case 46:
		return "NaN"
	case 47:
		return "Infinity"
	case 48:
		return "[1,2,3"
	case 49:
		return "\"\\\""
	case 50:
		return "\ufeff1"
	case 51:
		return "\"é\""
	case 52:
		return "[true,false,null]"
	case 53:
		return "{\"\":\"\"}"
	case 54:
		return "0.0e0"
	case 55:
		return "-0.5E-0"
	case 56:
		return "00"
	case 57:
		return "0x1"
	}
	return ""
}

// Valid reports json.Valid of three corpus entries and their concatenation
// with a space.
func Valid(i, j int) string {
	out := ""
	for _, s := range []string{corpus(i), corpus(j), corpus(i) + " " + corpus(j)} {
		if json.Valid([]byte(s)) {
			out += "T"
		} else {
			out += "F"
		}
	}
	return out
}

// Bytes reports json.Valid of raw bytes (invalid UTF-8 and control bytes).
func Bytes(b []byte) string {
	if json.Valid(b) {
		return "T"
	}
	return "F"
}
