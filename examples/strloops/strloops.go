// Package strloops is the ground truth for proofs over string parameters
// (issue #22): a string is quantified by its bytes, and the empty string
// (.str 0 0 0) by a companion theorem.
package strloops

// FirstByte returns the first byte of s, or -1 for the empty string.
func FirstByte(s string) int {
	if len(s) == 0 {
		return -1
	}
	return int(s[0])
}

// CountByte counts the bytes of s equal to c.
func CountByte(s string, c byte) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			n++
		}
	}
	return n
}
