package jsonvalid

func go2leanProbe(label string, v string) {}

// go2lean:spec Valid
func SpecValidDiff(i, j int, ret string) bool {
	go2leanProbe("valid", ret)
	return false
}

// go2lean:spec Bytes
func SpecBytesDiff(b []byte, ret string) bool {
	go2leanProbe("valid", ret)
	return false
}
