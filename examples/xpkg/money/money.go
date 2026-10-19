// Package money holds the helpers xpkg calls across the package boundary.
package money

// Pct is part as a percentage of total. Bug: total == 0 divides by zero.
func Pct(part, total int) int {
	return part * 100 / total
}

// Clamp bounds v to [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
