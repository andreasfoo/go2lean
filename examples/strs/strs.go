// Package strs is a ground-truth example for go2lean's string model.
package strs

import (
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
