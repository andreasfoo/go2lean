// Package convdiff checks go2lean's model of strconv against Go: every
// spec here fails on every input, so each search trial is a counterexample
// whose model value (a probe) M3 compares with the real one. Run with
// PKG=testdata/convdiff scripts/json-diff.sh.
package convdiff

import "strconv"

// corpus picks an edge case by index (the search covers small integers
// well; random strings rarely parse).
func corpus(i int) string {
	if i < 0 {
		i = -i
	}
	switch i % 42 {
	case 0:
		return ""
	case 1:
		return "0"
	case 2:
		return "-0"
	case 3:
		return "+7"
	case 4:
		return "-"
	case 5:
		return "+"
	case 6:
		return "0x1F"
	case 7:
		return "0X_1f"
	case 8:
		return "0x"
	case 9:
		return "0b101"
	case 10:
		return "0b_1"
	case 11:
		return "0o17"
	case 12:
		return "017"
	case 13:
		return "08"
	case 14:
		return "1_000"
	case 15:
		return "_1"
	case 16:
		return "1__0"
	case 17:
		return "1_"
	case 18:
		return "9223372036854775807"
	case 19:
		return "9223372036854775808"
	case 20:
		return "-9223372036854775808"
	case 21:
		return "-9223372036854775809"
	case 22:
		return "18446744073709551615"
	case 23:
		return "18446744073709551616"
	case 24:
		return "zz"
	case 25:
		return "Zz"
	case 26:
		return "128"
	case 27:
		return "-128"
	case 28:
		return "-129"
	case 29:
		return "255"
	case 30:
		return "256"
	case 31:
		return "1e3"
	case 32:
		return " 1"
	case 33:
		return "0_x1"
	case 34:
		return "t"
	case 35:
		return "True"
	case 36:
		return "FALSE"
	case 37:
		return "yes"
	case 38:
		return "1"
	case 39:
		return "f"
	case 40:
		return "-12a"
	case 41:
		return "7fffffff"
	}
	return ""
}

func show(v string, err error) string {
	if err != nil {
		return v + "!"
	}
	return v
}

// Int parses with base and bit size combinations.
func Int(i int) string {
	s := corpus(i)
	out := ""
	for _, c := range [][2]int{{10, 64}, {0, 64}, {16, 32}, {0, 8}, {2, 16}, {36, 0}, {1, 64}, {10, 65}} {
		n, err := strconv.ParseInt(s, c[0], c[1])
		out += show(strconv.FormatInt(n, 10), err) + ";"
	}
	return out
}

// Uint parses unsigned with base and bit size combinations.
func Uint(i int) string {
	s := corpus(i)
	out := ""
	for _, c := range [][2]int{{10, 64}, {0, 64}, {16, 8}, {0, 16}, {8, 0}} {
		n, err := strconv.ParseUint(s, c[0], c[1])
		out += show(strconv.FormatUint(n, 10), err) + ";"
	}
	return out
}

// Bool is strconv.ParseBool.
func Bool(i int) string {
	b, err := strconv.ParseBool(corpus(i))
	if b {
		return show("T", err)
	}
	return show("F", err)
}

// Format renders an integer in several bases.
func Format(n int64) string {
	return strconv.FormatInt(n, 2) + "," + strconv.FormatInt(n, 16) + "," + strconv.FormatInt(n, 36) + "," + strconv.FormatUint(uint64(n), 7)
}

// Round parses what FormatInt wrote.
func Round(n int64, base int) string {
	if base < 2 || base > 36 {
		base = 10
	}
	m, err := strconv.ParseInt(strconv.FormatInt(n, base), base, 64)
	return show(strconv.FormatInt(m, 10), err)
}
