// Package tables is a ground-truth example for lookup tables: package-level
// maps the initializer fills with constants and nothing else writes.
package tables

var budgets = map[string]int{"low": 1024, "medium": 4096, "high": 16384}

// levels is another name for the table (as tingly-box's typ package
// re-exports thinking.BudgetMapping).
var levels = budgets

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
	_, ok := levels[level]
	return ok
}

// Half is half the budget of a known level, read through the alias. Bug:
// "low" is not a multiple of 3, so the check panics.
func Half(level string) int {
	b, ok := levels[level]
	if ok && b%3 != 0 && b < 2048 {
		panic("odd budget")
	}
	return b / 2
}
