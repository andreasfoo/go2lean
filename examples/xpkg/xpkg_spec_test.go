package xpkg

// go2lean:spec Bounded
// SpecBoundedInRange: the result lies within 0..10.
func SpecBoundedInRange(v int, ret int) bool {
	return 0 <= ret && ret <= 10
}
