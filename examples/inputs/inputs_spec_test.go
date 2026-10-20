package inputs

// go2lean:spec Count
// SpecCountBounded: never more than there are entries.
func SpecCountBounded(extras map[string]any, ret int) bool {
	return ret >= 0 && ret <= len(extras)
}

// go2lean:spec Text
// SpecTextShorter: a decoded string is shorter than its JSON literal by at
// least the two quotes (escapes only shrink), and null decodes to "".
func SpecTextShorter(raw []byte, ret string) bool {
	return ret == "?" || len(ret)+2 <= len(raw) || (ret == "" && len(raw) >= 4)
}
