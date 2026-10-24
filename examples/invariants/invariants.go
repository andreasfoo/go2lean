// Package invariants is the ground truth for proofs of loops an agent
// writes (issue #22): small functions with one loop each, whose
// postconditions hold for every input. lean/Proofs/Functional.lean proves
// other functions by hand; these have no hand proof.
package invariants

// CountPositive returns how many elements of xs are positive.
func CountPositive(xs []int) int {
	n := 0
	for _, x := range xs {
		if x > 0 {
			n++
		}
	}
	return n
}

// IndexOf returns the index of the first element of xs equal to x, or -1.
func IndexOf(xs []int, x int) int {
	for i, y := range xs {
		if y == x {
			return i
		}
	}
	return -1
}

// AllNonNeg reports whether no element of xs is negative.
func AllNonNeg(xs []int) bool {
	for _, x := range xs {
		if x < 0 {
			return false
		}
	}
	return true
}

// Fill sets every element of xs to v.
func Fill(xs []int, v int) {
	for i := range xs {
		xs[i] = v
	}
}
