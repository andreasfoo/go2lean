package strs

// go2lean:spec Before
// SpecBeforeBytewise: a < b exactly when, at the first differing byte, a's
// is smaller, or a is a proper prefix of b.
func SpecBeforeBytewise(a, b string, ret bool) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return ret == (a[i] < b[i])
		}
	}
	return ret == (len(a) < len(b))
}
