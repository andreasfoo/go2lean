// Package globals is a ground-truth package for package-level variables
// nothing writes: a load of one sees its zero value.
package globals

// none is the zero id; nothing assigns it.
var none [4]byte

// table is never filled.
var table []int

// Blank reports whether id is the zero id.
func Blank(id [4]byte) bool { return id == none }

// At returns entry i of the table. Bug: the table is never filled, so
// every index is out of range.
func At(i int) int { return table[i] }

// gen is set by the package initializer; only a test helper replaces it.
var gen = defaultGen

func defaultGen() int { return 7 }

// SetGenForTest swaps the generator in tests.
func SetGenForTest(f func() int) { gen = f }

// Slot returns the slot the generator picks. Bug: the generator yields 7,
// past the four slots.
func Slot(slots [4]int) int { return slots[gen()] }

// Key is a comparable struct holding a string.
type Key struct {
	Name string
	N    int
}

// Same reports whether two keys are equal (== on a struct with a string).
func Same(a, b Key) bool { return a == b }
