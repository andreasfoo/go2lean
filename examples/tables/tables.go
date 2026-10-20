// Package tables is a ground-truth example for lookup tables: package-level
// maps the initializer fills with constants and nothing else writes.
package tables

var budgets = map[string]int{"low": 1024, "medium": 4096, "high": 16384}

// Share is how many medium budgets fit in max at a level, counted from the
// medium one. Bug: at "medium" the divisor is zero.
func Share(level string, max int) int {
	b, ok := budgets[level]
	if !ok {
		return 0
	}
	return max / (b - budgets["medium"])
}

// Known says whether a level has a budget (holds: the table is read only).
func Known(level string) bool {
	_, ok := budgets[level]
	return ok
}
