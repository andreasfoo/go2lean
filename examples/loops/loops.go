// Package loops is the ground truth for proofs harder than one loop over
// a slice (issue #22): a loop with a call in it, nested loops, nested loops
// that write the slice, and Go specs as the postconditions. None has a
// hand proof.
package loops

// Clamp returns v limited to [lo, hi] (for lo <= hi).
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// SpecClamp: for lo <= hi the result lies in [lo, hi].
//
// go2lean:spec Clamp
func SpecClamp(v, lo, hi int, ret int) bool {
	if lo > hi {
		return true
	}
	return lo <= ret && ret <= hi
}

// ClampAll clamps every element of xs to [lo, hi], calling Clamp.
func ClampAll(xs []int, lo, hi int) {
	for i := range xs {
		xs[i] = Clamp(xs[i], lo, hi)
	}
}

// SpecClampAll: for lo <= hi every element ends in [lo, hi].
//
// go2lean:spec ClampAll
func SpecClampAll(xs []int, lo, hi int) bool {
	if lo > hi {
		return true
	}
	for _, x := range xs {
		if x < lo || x > hi {
			return false
		}
	}
	return true
}

// HasDuplicate reports whether two elements of xs are equal.
func HasDuplicate(xs []int) bool {
	for i := range xs {
		for j := i + 1; j < len(xs); j++ {
			if xs[i] == xs[j] {
				return true
			}
		}
	}
	return false
}

// BubbleSort sorts a in place, ascending.
func BubbleSort(a []int) {
	n := len(a)
	for i := 0; i < n; i++ {
		for j := 0; j+1 < n-i; j++ {
			if a[j] > a[j+1] {
				a[j], a[j+1] = a[j+1], a[j]
			}
		}
	}
}

// SpecBubbleSortSorted: the slice ends sorted.
//
// go2lean:spec BubbleSort
func SpecBubbleSortSorted(a []int) bool {
	for i := 1; i < len(a); i++ {
		if a[i-1] > a[i] {
			return false
		}
	}
	return true
}
