// Package recs is a ground-truth package for slices whose elements are
// structs, strings and pointers, and for append.
package recs

// Item is a priced record.
type Item struct {
	Name  string
	Price int
	Qty   int
}

// Total sums Price*Qty over the items.
func Total(items []Item) int {
	t := 0
	for _, it := range items {
		t += it.Price * it.Qty
	}
	return t
}

// SpecTotal: the total of a single-item order is that item's line price.
func SpecTotal(items []Item, ret int) bool {
	if len(items) != 1 {
		return true
	}
	return ret == items[0].Price*items[0].Qty
}

// First returns the first item. Bug: it does not check for an empty slice.
func First(items []Item) Item {
	return items[0]
}

// Names collects the item names, appending one by one.
func Names(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

// SpecNames: the result has one name per item.
func SpecNames(items []Item, ret []string) bool {
	return len(ret) == len(items)
}

// Longest returns the longest string. Bug: a single empty string is
// shorter than the initial best, so the loop never picks it, and the
// result for [""] is "" either way; but for an empty input the function
// indexes out of range.
func Longest(ss []string) string {
	best := ss[0]
	for _, s := range ss[1:] {
		if len(s) > len(best) {
			best = s
		}
	}
	return best
}

// Push appends x and returns the new length. Bug: it keeps the result of
// append in a local, so a reallocation is lost and the length is wrong
// for the caller's slice — but the returned count is still right. The
// spec below checks the count, which holds, so Push is expected to pass.
func Push(xs []int, x int) ([]int, int) {
	xs = append(xs, x)
	return xs, len(xs)
}

// SpecPush: the count is one more than the input length.
func SpecPush(xs []int, x int, ret0 []int, ret1 int) bool {
	return ret1 == len(xs)+1 && len(ret0) == ret1
}

// Dedup drops adjacent duplicate prices. Bug: compares with the wrong index
// and panics on a slice of length one.
func Dedup(items []Item) []Item {
	out := items[:1]
	for i := 1; i <= len(items); i++ {
		if items[i].Price != items[i-1].Price {
			out = append(out, items[i])
		}
	}
	return out
}
