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

// Unwrap returns the int inside v. Bug: a nil v fails the assertion.
func Unwrap(v any) int {
	return v.(int)
}

// Hooks are optional callbacks.
type Hooks struct {
	Filter func(string) bool
}

// Pipe keeps the strings its filter accepts.
type Pipe struct {
	hooks *Hooks
	kept  []string
}

// Keep appends s unless a filter is set and rejects it.
func (p *Pipe) Keep(s string) {
	if p.hooks != nil && p.hooks.Filter != nil && !p.hooks.Filter(s) {
		return
	}
	p.kept = append(p.kept, s)
}
