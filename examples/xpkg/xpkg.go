// Package xpkg is a ground-truth package for calls across package
// boundaries: its bug and its certificate both live in the callee package
// money, so they are only visible when the models are linked.
package xpkg

import "github.com/andreasfoo/go2lean/examples/xpkg/money"

// Share guards negative totals but not zero: money.Pct then divides by
// zero.
func Share(part, total int) int {
	if total < 0 {
		return 0
	}
	return money.Pct(part, total)
}

// Bounded keeps v within 0..10 through money.Clamp.
func Bounded(v int) int {
	return money.Clamp(v, 0, 10)
}
