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
