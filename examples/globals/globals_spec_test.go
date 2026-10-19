package globals

// go2lean:spec Blank
// SpecBlankIsZero: an id is blank exactly when all its bytes are zero.
func SpecBlankIsZero(id [4]byte, ret bool) bool {
	return ret == (id[0] == 0 && id[1] == 0 && id[2] == 0 && id[3] == 0)
}

// go2lean:spec Same
// SpecSameFieldwise: keys are equal exactly when their fields are.
func SpecSameFieldwise(a, b Key, ret bool) bool {
	return ret == (a.Name == b.Name && a.N == b.N)
}
