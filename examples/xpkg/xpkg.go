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

// Units reads an amount through a method that divides by its unexported
// scale plus one: a test (or any caller outside money) cannot set the
// scale, so it is 0 and the division is safe.
func Units(a money.Amount) int { return a.PerScale() }
