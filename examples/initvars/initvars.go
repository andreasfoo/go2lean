// Package initvars is a ground-truth package for package-level variables an
// init function sets: a regexp compiled there (from a constant expression
// and from fmt.Sprintf of constants), a table of functions, a constant;
// and for data tables the variable initializers build: a slice literal,
// an append of it, an array of struct literals. Code that runs after init
// sees those values, never the zero value.
package initvars

import (
	"fmt"
	"regexp"
	"strconv"
)

const digits = `[0-9]+`

var (
	pairRe   *regexp.Regexp
	tripleRe *regexp.Regexp
	ops      map[string]func(int, int) int
	limit    int
	mode     int
)

func add(a, b int) int { return a + b }
func sub(a, b int) int { return a - b }

func init() {
	pairRe = regexp.MustCompile("^(" + digits + ")-(" + digits + ")$")
	tripleRe = regexp.MustCompile(fmt.Sprintf(`^(%s)\.(%s)\.(%s)$`, digits, digits, digits))
	ops = map[string]func(int, int) int{"+": add, "-": sub}
	limit = 3
}

func init() {
	if limit > 2 {
		mode = 1 // set on one branch only: unknown to the model
	}
}

// Pair parses "a-b". It never panics: the regexp has two groups, so a
// match has three elements.
func Pair(s string) (int, int, bool) {
	m := pairRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a, b, true
}

// Last returns the last part of "a.b.c". Bug: it reads group 4 of a
// regexp with three groups.
func Last(s string) string {
	m := tripleRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[4]
}

// Apply applies the named operation. It never panics: an unknown name has
// no entry and is refused.
func Apply(op string, a, b int) (int, bool) {
	f, ok := ops[op]
	if !ok {
		return 0, false
	}
	return f(a, b), true
}

// Slot returns slot i when i is below the limit. Bug: the limit is 3 and
// there are only 2 slots.
func Slot(slots [2]int, i int) int {
	if i < 0 || i >= limit {
		return -1
	}
	return slots[i]
}

var errEmpty = fmt.Errorf("empty range")

// Range is built by NewRange only.
type Range struct{ lo, hi int }

// NewRange returns the range [lo, hi], or an error when it is empty.
func NewRange(lo, hi int) (*Range, error) {
	if lo > hi {
		return nil, errEmpty
	}
	return &Range{lo, hi}, nil
}

// Contains reports whether i is in the range. It never panics: a range
// that failed to build is never used (value receiver, constructor with an
// error).
func (r Range) Contains(i int) bool { return r.lo <= i && i <= r.hi }

var (
	stops    = []rune{'.', ','}
	allStops = append(stops, ';')
	bands    = [3]struct{ lo, hi byte }{{0, 9}, {10, 99}, {100, 255}}
	widths   = []int{10, 90, 156}
)

// EndsClause reports whether s ends with a stop character. It never
// panics: allStops has three elements.
func EndsClause(s string) bool {
	if s == "" {
		return false
	}
	r := rune(s[len(s)-1])
	return allStops[0] == r || allStops[1] == r || allStops[2] == r
}

// LastStop returns the last stop character. Bug: stops has two elements;
// the third is only in allStops.
func LastStop() rune { return stops[2] }

// band returns the band b falls in.
func band(b byte) int {
	for i := range bands {
		if bands[i].lo <= b && b <= bands[i].hi {
			return i
		}
	}
	return -1
}

// BandWidth returns the width of b's band. It never panics: the bands
// cover every byte, so band never returns -1.
func BandWidth(b byte) int { return widths[band(b)] }

// units is a table of strings: in the model a string is a heap object, so
// the table is not a constant value.
var units = []string{"B", "KB", "MB"}

// Unit names the unit of 1024^i, "" past the last one. It never panics.
func Unit(i int) string {
	if i < 0 || i >= len(units) {
		return ""
	}
	return units[i]
}
