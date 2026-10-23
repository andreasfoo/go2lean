// Package fuzzed is a ground-truth package for fuzz targets read as specs
// (internal/fuzzimport): its properties are only in fuzzed_test.go.
package fuzzed

const digits = "0123456789abcdef"

// Encode writes n as four lower-case hex digits.
func Encode(n uint16) string {
	b := make([]byte, 4)
	for i := 3; i >= 0; i-- {
		b[i] = digits[n%16]
		n /= 16
	}
	return string(b)
}

// Decode reads one to four hex digits. Bug: 'f' is read as 14, so a value
// with an f digit does not survive Encode then Decode.
func Decode(s string) (uint16, bool) {
	if len(s) == 0 || len(s) > 4 {
		return 0, false
	}
	var n uint16
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d uint16
		switch {
		case '0' <= c && c <= '9':
			d = uint16(c - '0')
		case 'a' <= c && c <= 'e':
			d = uint16(c-'a') + 10
		case c == 'f':
			d = 14
		default:
			return 0, false
		}
		n = n*16 + d
	}
	return n, true
}
