package jsondiff

func go2leanProbe(label string, v string) {}

// go2lean:spec Encode
func SpecEncodeDiff(d Doc, ret string) bool {
	go2leanProbe("enc", ret)
	return false
}

// go2lean:spec Decode
func SpecDecodeDiff(s string, ret string) bool {
	go2leanProbe("dec", ret)
	return false
}

// go2lean:spec DecodeLoose
func SpecDecodeLooseDiff(s string, ret string) bool {
	go2leanProbe("dec", ret)
	return false
}

// go2lean:spec EncodeRaw
func SpecEncodeRawDiff(r []byte, ret string) bool {
	go2leanProbe("enc", ret)
	return false
}
