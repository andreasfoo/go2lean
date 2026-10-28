package stringcmpdiff

func go2leanProbe(label string, v string) {}

// go2lean:spec Compare
func SpecCompareDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}
