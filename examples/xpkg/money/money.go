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

// Amount is a sum of money; its scale is set only by NewAmount.
type Amount struct {
	Units int
	scale int
}

// NewAmount makes an amount with a scale.
func NewAmount(units, scale int) Amount { return Amount{units, scale} }

// PerScale divides the units by one more than the scale.
func (a Amount) PerScale() int { return a.Units / (a.scale + 1) }

// Ledger records amounts per account; only NewLedger makes a usable one.
type Ledger struct {
	Name    string
	entries map[string]int
}

// NewLedger makes a ledger.
func NewLedger(name string) *Ledger { return &Ledger{Name: name, entries: map[string]int{}} }

// Add records v under account k.
func (l *Ledger) Add(k string, v int) { l.entries[k] += v }
