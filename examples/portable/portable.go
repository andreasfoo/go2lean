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
// and m[2] is out of range on every match.
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
