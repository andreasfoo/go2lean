// Package portable is a ground-truth example from running go2lean on code
// it was not developed on (one-api, spf13/cast): each function stands for a
// construct whose model gave a spurious or missed result there.
package portable

import (
	"errors"
	"math/rand"
	"regexp"
	"strconv"

	"github.com/andreasfoo/go2lean/examples/portable/meta"
)

// URL is the first byte of the base URL. Bug: panics on an empty BaseURL.
// A pointer to a struct of exported fields from another package is data,
// even when that package has a function returning one.
func URL(m *meta.Meta) string {
	return m.BaseURL[:1]
}

// Digit is a random decimal digit: rand.Intn(n) is in [0, n), so the index
// is always in range (holds).
func Digit() byte {
	const digits = "0123456789"
	return digits[rand.Intn(len(digits))]
}

// Pick is a random element. Bug: rand.Intn panics on an empty slice.
func Pick(xs []string) string {
	return xs[rand.Intn(len(xs))]
}

// Parse divides by a parsed count. Bug: "0" and anything not a number give
// zero. parse, below, has the same name up to case: each is a target.
func Parse(s string) int { return parse(s) + 1 }

func parse(s string) int {
	n, _ := strconv.Atoi(s)
	return 10 / n
}

// PerMax is how many of a limit fit in 1000. A limit out of the int64
// range parses to the nearest bound (with an error), not to zero, so the
// division is safe (holds).
func PerMax() int {
	n, _ := strconv.Atoi("9223372036854775808")
	return 1000 / n
}

// Must returns v or panics with err. Generic: its instances are modelled,
// not the function itself (Must[int] is used by Port).
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Port parses a port. Bug: panics on anything but a number.
func Port(s string) int {
	return Must(strconv.Atoi(s))
}

var kb = 1024
var mb = kb * 1024

// PerMB is how many megabytes n bytes are: mb is a constant expression
// over another package variable, never zero (holds).
func PerMB(n int) int { return n / mb }

var intPart = regexp.MustCompile(`^(\d+)\.`)

// IntPart is the integer part of a decimal: the pattern has one group, so
// a match has two elements (holds).
func IntPart(s string) string {
	if m := intPart.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return s
}

// Unit is the unit of a size like "10kb". Bug: the pattern has one group,
// and m[2] is out of range on every match (the model runs the pattern, and
// the search draws strings it matches).
func Unit(s string) string {
	if m := regexp.MustCompile(`^\d+([a-z]+)$`).FindStringSubmatch(s); m != nil {
		return m[2]
	}
	return ""
}

// ErrEmpty is a sentinel error.
var ErrEmpty = errors.New("empty")

func first(xs []int) (int, error) {
	if len(xs) == 0 {
		return 0, ErrEmpty
	}
	return xs[0], nil
}

// Count is ten over the length: the sentinel check covers the empty slice
// (holds; errors.Is on a sentinel the model returned is exact).
func Count(xs []int) int {
	if _, err := first(xs); errors.Is(err, ErrEmpty) {
		return 0
	}
	return 10 / len(xs)
}

// Head is a hundred over the first element. Bug: a first element of 0.
func Head(xs []int) int {
	v, err := first(xs)
	if err == ErrEmpty {
		return -1
	}
	return 100 / v
}

// Per is a hundred over a count of any integer type, cast-style. Bug: a
// uint8 count of 0 (an input of type any reaches every case the switch
// names).
func Per(v any) int {
	switch x := v.(type) {
	case int64:
		if x == 0 {
			return 0
		}
		return int(100 / x)
	case uint8:
		return 100 / int(x)
	case string:
		return len(x)
	}
	return -1
}

var (
	verRe  = regexp.MustCompile(`^v(\d+)\.(\d+)$`)
	dateRe = regexp.MustCompile(`^(\d{4})-(\d\d)$`)
)

// group is the n-th group of the first match of re in s, or "".
func group(re *regexp.Regexp, s string, n int) string {
	if m := re.FindStringSubmatch(s); m != nil && n < len(m) {
		return m[n]
	}
	return ""
}

// Minor is a hundred over the minor version of "v1.2". Bug: "v1.0" (the
// regexp reaches group through a parameter: picked at run time).
func Minor(s string) int {
	n, err := strconv.Atoi(group(verRe, s, 2))
	if err != nil {
		return 0
	}
	return 100 / n
}

// Month is the month of "2024-05" as an index into twelve names (holds:
// the check covers both ends).
func Month(s string) string {
	n, err := strconv.Atoi(group(dateRe, s, 2))
	if err != nil || n < 1 || n > 12 {
		return ""
	}
	return "JFMAMJJASOND"[n-1 : n]
}

// Label tags a name with the session's model. Bug: an empty name divides
// by zero (a field read through a handle is havoc, not the end of the run).
func Label(s *meta.Session, name string) string {
	return s.Model + "/" + strconv.Itoa(100/len(name))
}
