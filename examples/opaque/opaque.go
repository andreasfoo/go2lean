// Package opaque is a ground-truth package for parameters the model does
// not look into: contexts, maps, interfaces and structs holding them.
package opaque

import "context"

// Cache holds counters behind a map the model treats as opaque.
type Cache struct {
	entries map[string]int
	limit   int
	name    string
}

// Share returns n as a share of the limit. Bug: limit may be zero.
func (c *Cache) Share(n int) int {
	return n * 100 / c.limit
}

// Lookup reads the first key. Bug: keys may be empty; the context and the
// map are irrelevant to the panic, and the test passes context.Background()
// and a nil map.
func Lookup(ctx context.Context, m map[string]int, keys []string) int {
	if ctx.Err() != nil {
		return -1
	}
	return m[keys[0]]
}

// Describe is safe: it never indexes anything.
func Describe(v any, m map[string]int) string {
	if v == nil && len(m) == 0 {
		return "empty"
	}
	return "set"
}
