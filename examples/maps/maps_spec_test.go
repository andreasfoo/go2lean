package maps

// go2lean:spec Distinct
// SpecDistinctBounded: there are at most as many distinct words as words,
// and at least one when there is a word.
func SpecDistinctBounded(words []string, ret int) bool {
	if ret > len(words) {
		return false
	}
	return len(words) == 0 || ret >= 1
}

// go2lean:spec Sum
// SpecSumNonNegative: the values stored are positions plus one, so the
// sum is never negative.
func SpecSumNonNegative(keys []string, ret int) bool {
	return ret >= 0
}
