package convdiff

func go2leanProbe(label string, v string) {}

// go2lean:spec Int
func SpecIntDiff(i int, ret string) bool {
	go2leanProbe("conv", ret)
	return false
}

// go2lean:spec Uint
func SpecUintDiff(i int, ret string) bool {
	go2leanProbe("conv", ret)
	return false
}

// go2lean:spec Bool
func SpecBoolDiff(i int, ret string) bool {
	go2leanProbe("conv", ret)
	return false
}

// go2lean:spec Format
func SpecFormatDiff(n int64, ret string) bool {
	go2leanProbe("conv", ret)
	return false
}

// go2lean:spec Round
func SpecRoundDiff(n int64, base int, ret string) bool {
	go2leanProbe("conv", ret)
	return false
}
