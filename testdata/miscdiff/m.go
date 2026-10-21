// Package miscdiff checks go2lean's models of reflect.Value's Kind and
// IsNil and of unicode/utf16.Encode against Go: every spec here fails on
// every input, so each search trial is a counterexample whose model value
// (a probe) M3 compares with the real one. Run with
// PKG=testdata/miscdiff scripts/json-diff.sh.
package miscdiff

import (
	"reflect"
	"strconv"
	"unicode/utf16"
)

type pair struct{ A, B int }

// value boxes one of a set of values of different kinds.
func value(i int) any {
	n := 3
	switch i % 14 {
	case 0:
		return nil
	case 1:
		return "s"
	case 2:
		return 7
	case 3:
		return int8(-1)
	case 4:
		return uint64(9)
	case 5:
		return true
	case 6:
		return (*int)(nil)
	case 7:
		return &n
	case 8:
		return []int(nil)
	case 9:
		return []int{1}
	case 10:
		return map[string]int(nil)
	case 11:
		return map[string]int{"a": 1}
	case 12:
		return pair{1, 2}
	}
	return [2]int{1, 2}
}

func kind(v any) string {
	rv := reflect.ValueOf(v)
	k := rv.Kind()
	out := strconv.Itoa(int(k))
	switch k {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		if rv.IsNil() {
			out += "n"
		}
	}
	return out
}

// Kind is the kind (and nil-ness) of a boxed value.
func Kind(i int) string {
	if i < 0 {
		i = -i
	}
	return kind(value(i))
}

// KindAny is the kind of a generated interface value.
func KindAny(v any) string { return kind(v) }

// UTF16 encodes runes, valid and not, and a string's runes.
func UTF16(r1, r2 int32, s string) string {
	out := ""
	for _, u := range utf16.Encode(append([]rune{r1, r2, 0x1F600, 0xD800, 0x110000, -1}, []rune(s)...)) {
		out += strconv.Itoa(int(u)) + ","
	}
	return out
}
