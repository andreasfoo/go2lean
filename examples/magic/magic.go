// Package magic is a ground-truth example for go2lean: the bug hides behind
// a comparison with a magic constant, which blind random search almost never
// hits. The engines seed their value pools with the literals of the target.
package magic

// Decode reads the first payload element only for one specific tag. It
// indexes an empty payload when the tag matches.
func Decode(tag int, payload []int) int {
	if tag != 0x5EC4E7 {
		return 0
	}
	return payload[0]
}
