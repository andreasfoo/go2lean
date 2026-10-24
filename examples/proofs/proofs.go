// Package proofs is the ground-truth package for functional proofs: small
// functions whose specs hold for every input, proved over the model with
// Std.WP and vcgen (docs/related-work.md).
package proofs

// Max2 returns the larger of a and b.
func Max2(a, b int) int {
	if a < b {
		return b
	}
	return a
}

// CountUp counts from 0 up to n (0 for a negative n).
func CountUp(n int) int {
	i := 0
	for i < n {
		i++
	}
	return i
}

// Max returns the largest element of xs, or 0 for an empty slice.
func Max(xs []int) int {
	m := 0
	if len(xs) > 0 {
		m = xs[0]
	}
	for i := 1; i < len(xs); i++ {
		if xs[i] > m {
			m = xs[i]
		}
	}
	return m
}

// InsertionSort sorts a in place, ascending.
func InsertionSort(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// Max3 returns the largest of a, b and c, through Max2.
func Max3(a, b, c int) int {
	return Max2(Max2(a, b), c)
}
