// Package overflow is a ground-truth example for go2lean: Sum silently wraps
// on overflow, so the property "non-negative inputs give a non-negative sum"
// is false.
package overflow

// Sum returns the sum of xs. It wraps on overflow.
func Sum(xs []int) int {
	s := 0
	for i := 0; i < len(xs); i++ {
		s += xs[i]
	}
	return s
}

// SpecSum is the spec-in-Go oracle for Sum: when every element is
// non-negative, the sum must be non-negative. go2lean compiles it into the
// model with the same pipeline as Sum, so the Lean property and this Go
// oracle agree by construction.
//
// go2lean:spec Sum
func SpecSum(xs []int, ret int) bool {
	for i := 0; i < len(xs); i++ {
		if xs[i] < 0 {
			return true
		}
	}
	return ret >= 0
}
