// Package hang is the ground truth for loops that never end on some
// inputs (#86): no property is written for them, but a run that comes
// back to a state it was in loops forever.
package hang

// Words counts the space-separated words of s. A tab is skipped without
// moving on, so a word that starts with a tab is never left.
func Words(s string) int {
	n, i := 0, 0
	for i < len(s) {
		if s[i] == ' ' {
			i++
			continue
		}
		if s[i] == '\t' {
			continue
		}
		n++
		for i < len(s) && s[i] != ' ' {
			i++
		}
	}
	return n
}

// Last follows the links next[i] from start to the last element of its
// chain (a link out of range ends it). A chain that links back into
// itself is followed forever.
func Last(next []int, start int) int {
	if start < 0 || start >= len(next) {
		return -1
	}
	i := start
	for {
		j := next[i]
		if j < 0 || j >= len(next) {
			return i
		}
		i = j
	}
}

// Triangle adds 0 + 1 + ... + n-1. It ends on every input, but a large n
// runs out of fuel; none of its states repeats, so it is no loop.
func Triangle(n int) int {
	t := 0
	for i := 0; i < n; i++ {
		t += i
	}
	return t
}
