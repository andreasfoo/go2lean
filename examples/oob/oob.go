// Package oob is a ground-truth example for go2lean: Last has an off-by-one
// and indexes past the end of the slice.
package oob

// Last returns the last element of xs, or 0 for an empty slice. The bound
// check uses <= instead of <, so a non-empty slice panics.
func Last(xs []int) int {
	i := 0
	for i <= len(xs) {
		if i == len(xs)-1 {
			return xs[i+1]
		}
		i++
	}
	return 0
}

// Clamp returns v limited to [lo, hi]. It is correct and should produce no
// counterexample.
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// SpecClamp: the result lies within [lo, hi] whenever lo <= hi.
//
// go2lean:spec Clamp
func SpecClamp(v, lo, hi int, ret int) bool {
	if lo > hi {
		return true
	}
	return lo <= ret && ret <= hi
}
