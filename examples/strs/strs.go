// Package strs is a ground-truth example for go2lean's string model.
package strs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Scheme returns the part of u before "://". It slices with the result of
// strings.Index without checking for -1, so a URL without "://" panics.
func Scheme(u string) string {
	i := strings.Index(u, "://")
	return u[:i]
}

// ParsePort parses "port:NNNN". It forgets the upper bound, so values above
// 65535 are accepted.
func ParsePort(s string) (int, error) {
	if !strings.HasPrefix(s, "port:") {
		return 0, strconv.ErrSyntax
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "port:"))
	if err != nil || n < 0 {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}

// SpecParsePortRange: a successfully parsed port fits in 16 bits.
//
// go2lean:spec ParsePort
func SpecParsePortRange(s string, ret0 int, ret1 error) bool {
	if ret1 != nil {
		return true
	}
	return ret0 >= 0 && ret0 <= 65535
}

// IsAdmin is correct: only the exact name matches.
func IsAdmin(name string) bool {
	return strings.TrimSpace(name) == "admin"
}

// Label names an item by kind and number.
func Label(kind string, n int) string { return fmt.Sprintf("%s-%d", kind, n) }

// Short is the first eight bytes of the label. Bug: a short kind and a
// small number make a label under eight bytes, and the slice panics.
func Short(kind string, n int) string { return Label(kind, n)[:8] }

// TokenPrefix is the first eight hex digits of a random token. It holds:
// crypto/rand.Read never fails, so the token always has eight digits.
func TokenPrefix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		var none string
		return none[:8]
	}
	return hex.EncodeToString(b)[:8]
}

// HexTag is the first four hex digits of a key. Bug: a key under two
// bytes has fewer than four digits, and the slice panics.
func HexTag(key string) string { return fmt.Sprintf("%x", []byte(key))[:4] }

// go2lean:spec Label
// SpecLabelFormat: the kind, a dash, the number in decimal.
func SpecLabelFormat(kind string, n int, ret string) bool {
	return ret == kind+"-"+strconv.Itoa(n)
}

// Before reports whether a sorts before b (Go's byte-wise order).
func Before(a, b string) bool { return a < b }
