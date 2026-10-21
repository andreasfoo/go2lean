package urldiff

func go2leanProbe(label string, v string) {}

// go2lean:spec Parts
func SpecPartsDiff(i int, ret string) bool {
	go2leanProbe("url", ret)
	return false
}

// go2lean:spec Raw
func SpecRawDiff(s string, ret string) bool {
	go2leanProbe("url", ret)
	return false
}

// go2lean:spec Provider
func SpecProviderDiff(i int, ret string) bool {
	go2leanProbe("url", ret)
	return false
}
