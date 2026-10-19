// Package closures is a ground-truth package for func values: closures
// that capture variables, functions passed as values, calls through them.
package closures

import "slices"

// anyOf reports whether f holds for some element of xs.
func anyOf(xs []int, f func(int) bool) bool {
	for _, x := range xs {
		if f(x) {
			return true
		}
	}
	return false
}

func negative(x int) bool { return x < 0 }

// HasNeg reports whether xs holds a negative number (a function value).
func HasNeg(xs []int) bool { return anyOf(xs, negative) }

// HasAbove reports whether xs holds a number above limit (a closure).
func HasAbove(xs []int, limit int) bool {
	return slices.ContainsFunc(xs, func(x int) bool { return x > limit })
}

// Last returns the last doubled element a closure pushed. Bug: for an
// empty xs the closure never runs and out[len(out)-1] panics.
func Last(xs []int) int {
	var out []int
	push := func(x int) { out = append(out, x) }
	for _, x := range xs {
		push(x * 2)
	}
	return out[len(out)-1]
}

// Total sums xs through a closure that updates a captured counter.
func Total(xs []int) int {
	sum := 0
	add := func(x int) { sum += x }
	for _, x := range xs {
		add(x)
	}
	return sum
}
