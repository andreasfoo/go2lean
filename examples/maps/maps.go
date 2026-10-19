// Package maps is a ground-truth package for maps the program builds:
// lookups, updates, len, delete and range.
package maps

// Pick returns the name before want in names. Bug: the position of a
// missing name, or of the first one, is 0, and names[-1] panics.
func Pick(names []string, want string) string {
	pos := map[string]int{}
	for i, n := range names {
		pos[n] = i
	}
	return names[pos[want]-1]
}

// Distinct counts the different words.
func Distinct(words []string) int {
	seen := map[string]bool{}
	for _, w := range words {
		seen[w] = true
	}
	return len(seen)
}

// Registry maps keys to values.
type Registry struct {
	m map[string]int
}

// NewRegistry makes a registry with its map.
func NewRegistry() *Registry { return &Registry{m: map[string]int{}} }

// Put stores v under k. Bug: a Registry not made by NewRegistry has a nil
// map, and the write panics.
func (r *Registry) Put(k string, v int) { r.m[k] = v }

// Sum adds the values of the keys present, after dropping key "skip".
func Sum(keys []string) int {
	r := NewRegistry()
	for i, k := range keys {
		r.Put(k, i+1)
	}
	delete(r.m, "skip")
	total := 0
	for _, v := range r.m {
		total += v
	}
	return total
}
