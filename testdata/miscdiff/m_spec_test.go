package miscdiff

func go2leanProbe(label string, v string) {}

// go2lean:spec Kind
func SpecKindDiff(i int, ret string) bool {
	go2leanProbe("misc", ret)
	return false
}

// go2lean:spec KindAny
func SpecKindAnyDiff(v any, ret string) bool {
	go2leanProbe("misc", ret)
	return false
}

// go2lean:spec UTF16
func SpecUTF16Diff(r1, r2 int32, s string, ret string) bool {
	go2leanProbe("misc", ret)
	return false
}
