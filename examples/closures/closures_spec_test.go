package closures

// go2lean:spec HasNeg
// SpecHasNeg: true exactly when some element is negative.
func SpecHasNeg(xs []int, ret bool) bool {
	for _, x := range xs {
		if x < 0 {
			return ret
		}
	}
	return !ret
}

// go2lean:spec HasAbove
// SpecHasAbove: true exactly when some element is above the limit.
func SpecHasAbove(xs []int, limit int, ret bool) bool {
	for _, x := range xs {
		if x > limit {
			return ret
		}
	}
	return !ret
}

// go2lean:spec Total
// SpecTotal: the closure's updates to the captured sum are the sum.
func SpecTotal(xs []int, ret int) bool {
	s := 0
	for _, x := range xs {
		s += x
	}
	return ret == s
}
