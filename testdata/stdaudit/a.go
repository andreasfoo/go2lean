// Package stdaudit checks go2lean's models of strings, strconv and
// unicode/utf8 against Go,
// as testdata/rediff does for regexp: every spec fails, so each search trial
// is a counterexample whose model value (a probe) M3 compares with the real
// one. Run with PKG=testdata/stdaudit DEPS=1 scripts/json-diff.sh; with
// GO2LEAN_NO_STDLIB_OPS=strings,strconv it checks the translation of their
// source instead of the hand-written models (docs/semantics.md).
package stdaudit

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

func b(v bool) string {
	if v {
		return "T"
	}
	return "F"
}

func cut(s, t string) string {
	before, after, ok := strings.Cut(s, t)
	return before + "|" + after + "|" + b(ok)
}

func atoi(s string) string {
	n, err := strconv.Atoi(s)
	return strconv.Itoa(n) + "|" + b(err == nil)
}

func parseInt(s string, base, bits int) string {
	if base == 1 {
		base = 0
	}
	n, err := strconv.ParseInt(s, base, bits)
	return strconv.FormatInt(n, 10) + "|" + b(err == nil)
}

func parseUint(s string, base, bits int) string {
	if base == 1 {
		base = 0
	}
	n, err := strconv.ParseUint(s, base, bits)
	return strconv.FormatUint(n, 10) + "|" + b(err == nil)
}

func parseBool(s string) string {
	v, err := strconv.ParseBool(s)
	return b(v) + "|" + b(err == nil)
}

func HasPrefix(s, t string) string { return b(strings.HasPrefix(s, t)) }

func HasSuffix(s, t string) string { return b(strings.HasSuffix(s, t)) }

func Contains(s, t string) string { return b(strings.Contains(s, t)) }

func Index(s, t string) string { return strconv.Itoa(strings.Index(s, t)) }

func LastIndex(s, t string) string { return strconv.Itoa(strings.LastIndex(s, t)) }

func IndexByte(s string, c byte) string { return strconv.Itoa(strings.IndexByte(s, c)) }

func LastIndexByte(s string, c byte) string { return strconv.Itoa(strings.LastIndexByte(s, c)) }

func Count(s, t string) string { return strconv.Itoa(strings.Count(s, t)) }

func EqualFold(s, t string) string { return b(strings.EqualFold(s, t)) }

func TrimSpace(s string) string { return strings.TrimSpace(s) }

func TrimPrefix(s, t string) string { return strings.TrimPrefix(s, t) }

func TrimSuffix(s, t string) string { return strings.TrimSuffix(s, t) }

func Trim(s, t string) string { return strings.Trim(s, t) }

func TrimLeft(s, t string) string { return strings.TrimLeft(s, t) }

func TrimRight(s, t string) string { return strings.TrimRight(s, t) }

func ToLower(s string) string { return strings.ToLower(s) }

func ToUpper(s string) string { return strings.ToUpper(s) }

func ReplaceAll(s, t, u string) string { return strings.ReplaceAll(s, t, u) }

func Split(s, t string) string {
	return strings.Join(strings.Split(s, t), "|") + "#" + strconv.Itoa(len(strings.Split(s, t)))
}

func Fields(s string) string {
	return strings.Join(strings.Fields(s), "|") + "#" + strconv.Itoa(len(strings.Fields(s)))
}

func Repeat(s string, n uint8) string { return strings.Repeat(s, int(n%5)) }

func Cut(s, t string) string { return cut(s, t) }

func Atoi(s string) string { return atoi(s) }

func Itoa(n int) string { return strconv.Itoa(n) }

func ParseInt(s string, base uint8, bits uint8) string {
	return parseInt(s, int(base%37), int(bits%65))
}

func ParseUint(s string, base uint8, bits uint8) string {
	return parseUint(s, int(base%37), int(bits%65))
}

func ParseBool(s string) string { return parseBool(s) }

func FormatInt(n int64, base uint8) string { return strconv.FormatInt(n, 2+int(base%35)) }

func FormatUint(n uint64, base uint8) string { return strconv.FormatUint(n, 2+int(base%35)) }

func decodeRune(p []byte) string {
	r, n := utf8.DecodeRune(p)
	return strconv.Itoa(int(r)) + "|" + strconv.Itoa(n)
}

func decodeRuneInString(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return strconv.Itoa(int(r)) + "|" + strconv.Itoa(n)
}

func decodeLastRuneInString(s string) string {
	r, n := utf8.DecodeLastRuneInString(s)
	return strconv.Itoa(int(r)) + "|" + strconv.Itoa(n)
}

func runeCount(p []byte) string { return strconv.Itoa(utf8.RuneCount(p)) }

func validString(s string) string { return b(utf8.ValidString(s)) }

func decode3(a, c, d byte) string { return decodeRune([]byte{a, c, d}) }

// The search draws small bytes first: these put the lead byte above 0x7F.
func decodeHi(a, c, d byte) string { return decodeRune([]byte{0x80 | a, c, d}) }

func decodeStrHi(a, c, d byte) string {
	return decodeRuneInString(string([]byte{0x80 | a, 0x80 | c, d}))
}
