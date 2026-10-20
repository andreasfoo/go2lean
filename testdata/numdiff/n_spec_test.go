package numdiff

func go2leanProbe(label string, v string) {}

// go2lean:spec Any
func SpecAnyDiff(i, j int, ret string) bool {
	go2leanProbe("num", ret)
	return false
}

// go2lean:spec Field
func SpecFieldDiff(i, j int, ret string) bool {
	go2leanProbe("num", ret)
	return false
}
