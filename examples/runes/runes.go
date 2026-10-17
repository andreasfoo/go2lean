// Package runes is a ground-truth package for range over strings, rune
// conversions, fixed-size arrays, floats (opaque to the model) and defer.
package runes

import "sync"

// Reverse reverses a string rune by rune.
func Reverse(s string) string {
	rs := []rune(s)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	return string(rs)
}

// SpecReverse: reversing twice gives the input back for valid UTF-8; the
// spec only checks the rune count, which is the same for any input.
func SpecReverse(s string, ret string) bool {
	return len([]rune(ret)) == len([]rune(s))
}

// Initials takes the first rune of each word into a fixed array, guarded
// by its length; the range loop is modelled rune by rune (safe).
func Initials(s string) [4]rune {
	var buf [4]rune
	n := 0
	for pos, r := range s {
		if pos == 0 || s[pos-1] == ' ' {
			if n < len(buf) {
				buf[n] = r
			}
			n++
		}
	}
	return buf
}

// Counter is a guarded counter next to a float the model does not compute.
type Counter struct {
	mu    sync.Mutex
	n     int
	ratio float64
	hist  [3]int
}

// Bump adds to the counter under the lock; the deferred unlock is ignored
// by the model. Bug: the histogram index is n%4, one past the array.
func (c *Counter) Bump(by int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += by
	c.hist[c.n%4]++
	return c.n
}

// Head returns the first byte of a short string as a sub-slice of a
// fixed buffer. Bug: a 3-index slice with max < len panics.
func Head(s string) []byte {
	var buf [8]byte
	n := copy(buf[:], s)
	return buf[0:n:4]
}
