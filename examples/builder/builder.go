// Package builder is a ground-truth package for strings.Builder (a string
// cell in the model) and for errors built by errors.New / fmt.Errorf,
// which are never nil.
package builder

import (
	"fmt"
	"strconv"
	"strings"
)

// Join joins the parts with sep and drops one trailing space. Bug: an
// empty result is indexed at -1.
func Join(parts []string, sep string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(p)
	}
	s := b.String()
	if s[len(s)-1] == ' ' {
		return s[:len(s)-1]
	}
	return s
}

// Parse wraps strconv.Atoi's error.
func Parse(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty input")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad number %q: %w", s, err)
	}
	return n, nil
}

// SpecParseErrZero: a non-nil error comes with a zero result.
func SpecParseErrZero(s string, ret0 int, ret1 error) bool {
	return ret1 == nil || ret0 == 0
}

// SpecParseEmptyFails: the empty string is rejected.
func SpecParseEmptyFails(s string, ret0 int, ret1 error) bool {
	return s != "" || ret1 != nil
}

// Tag writes <name> into the caller's builder.
func Tag(b *strings.Builder, name string) {
	b.WriteByte('<')
	b.WriteString(name)
	b.WriteByte('>')
}

// SpecTagLen: the builder grew by len(name)+2 (it starts empty).
func SpecTagLen(b *strings.Builder, name string) bool {
	return b.Len() == len(name)+2
}
