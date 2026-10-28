// Package stringcmpdiff checks the native bytealg string comparison bridge
// against real Go, including byte strings that are not valid UTF-8.
package stringcmpdiff

import (
	"strconv"
	"strings"
)

func Compare(s, t string) string {
	out := strconv.Itoa(strings.Compare(s, t)) + "|" + strconv.Itoa(strings.Compare(t, s))
	edges := []string{"", "\x00", "\x00a", "a", "aa", "\x7f", "\x80", "\xff", "\xc0\x80", "é"}
	for _, a := range edges {
		for _, b := range edges {
			out += "|" + strconv.Itoa(strings.Compare(a, b))
		}
	}
	return out
}
