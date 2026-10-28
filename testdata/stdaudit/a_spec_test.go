package stdaudit

func go2leanProbe(label string, v string) {}

// go2lean:spec HasPrefix
func SpecHasPrefixDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec HasSuffix
func SpecHasSuffixDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Contains
func SpecContainsDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Index
func SpecIndexDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec LastIndex
func SpecLastIndexDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec IndexByte
func SpecIndexByteDiff(s string, c byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec LastIndexByte
func SpecLastIndexByteDiff(s string, c byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Count
func SpecCountDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec EqualFold
func SpecEqualFoldDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec TrimSpace
func SpecTrimSpaceDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec TrimPrefix
func SpecTrimPrefixDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec TrimSuffix
func SpecTrimSuffixDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Trim
func SpecTrimDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec TrimLeft
func SpecTrimLeftDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec TrimRight
func SpecTrimRightDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec ToLower
func SpecToLowerDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec ToUpper
func SpecToUpperDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec ReplaceAll
func SpecReplaceAllDiff(s, t, u string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Split
func SpecSplitDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Fields
func SpecFieldsDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Repeat
func SpecRepeatDiff(s string, n uint8, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Cut
func SpecCutDiff(s, t string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Atoi
func SpecAtoiDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Itoa
func SpecItoaDiff(n int, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec ParseInt
func SpecParseIntDiff(s string, base uint8, bits uint8, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec ParseUint
func SpecParseUintDiff(s string, base uint8, bits uint8, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec ParseBool
func SpecParseBoolDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec FormatInt
func SpecFormatIntDiff(n int64, base uint8, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec FormatUint
func SpecFormatUintDiff(n uint64, base uint8, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec decodeRune
func SpecDecodeRuneDiff(p []byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec decodeRuneInString
func SpecDecodeRuneInStringDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec decodeLastRuneInString
func SpecDecodeLastRuneInStringDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec runeCount
func SpecRuneCountDiff(p []byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec validString
func SpecValidStringDiff(s string, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec decode3
func SpecDecode3Diff(a, c, d byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec decodeHi
func SpecDecodeHiDiff(a, c, d byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec decodeStrHi
func SpecDecodeStrHiDiff(a, c, d byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Uvarint4
func SpecUvarint4Diff(a, c, d, e byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Uvarint11
func SpecUvarint11Diff(a, e byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec Varint3
func SpecVarint3Diff(a, c, e byte, ret string) bool {
	go2leanProbe("v", ret)
	return false
}

// go2lean:spec PutUvarint
func SpecPutUvarintDiff(x uint64, ret string) bool {
	go2leanProbe("v", ret)
	return false
}
