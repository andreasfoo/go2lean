// Package iface is a ground-truth package for interface values: values
// stored in an `any` and asserted back to their concrete type.
package iface

// Queue holds items of any type.
type Queue struct{ items []any }

// Push appends an int.
func (q *Queue) Push(v int) { q.items = append(q.items, v) }

// PushName appends a string.
func (q *Queue) PushName(s string) { q.items = append(q.items, s) }

// Sum adds the items, asserting each to int.
func (q *Queue) Sum() int {
	total := 0
	for _, it := range q.items {
		total += it.(int)
	}
	return total
}

// Ints counts the int items with a comma-ok assertion.
func (q *Queue) Ints() int {
	n := 0
	for _, it := range q.items {
		if _, ok := it.(int); ok {
			n++
		}
	}
	return n
}

// Mixed builds a queue and sums it. Bug: a non-empty name puts a string in
// the queue, and Sum's assertion panics on it.
func Mixed(n int, name string) int {
	q := &Queue{}
	q.Push(n)
	if name != "" {
		q.PushName(name)
	}
	return q.Sum()
}

// Count builds the same queue and counts its ints, which never panics.
func Count(n int, name string) int {
	q := &Queue{}
	q.Push(n)
	if name != "" {
		q.PushName(name)
	}
	return q.Ints()
}
