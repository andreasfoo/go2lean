// Package divzero is a ground-truth example for go2lean: Avg divides by a
// caller-controlled count.
package divzero

// Avg returns total / count. count == 0 panics.
func Avg(total, count int) int {
	return total / count
}

// Ratio guards the divisor but not the MinInt / -1 case, which in Go wraps
// rather than panicking; it must not be reported as a panic.
func Ratio(a, b int) int {
	if b == 0 {
		return 0
	}
	return a / b
}
