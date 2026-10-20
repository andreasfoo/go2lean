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

// go2lean:spec DecodeSchema
func SpecDecodeSchemaDiff(s string, ret string) bool {
	go2leanProbe("schema", ret)
	return false
}

// go2lean:spec DecodeCitations
func SpecDecodeCitationsDiff(s string, ret string) bool {
	go2leanProbe("citations", ret)
	return false
}

// go2lean:spec Encode64
func SpecEncode64Diff(s string, ret string) bool {
	go2leanProbe("enc64", ret)
	return false
}

// go2lean:spec DecodeURL64
func SpecDecodeURL64Diff(s string, ret string) bool {
	go2leanProbe("url64", ret)
	return false
}

// go2lean:spec Decode64
func SpecDecode64Diff(s string, ret string) bool {
	go2leanProbe("b64", ret)
	return false
}
