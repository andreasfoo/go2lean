// Package numdiff checks go2lean's model of JSON numbers decoded into
// interfaces and float64 fields, then encoded again, against Go: every spec
// here fails on every input, so each search trial is a counterexample whose
// model value (a probe) M3 compares with the real one. Run with
// PKG=testdata/numdiff scripts/json-diff.sh.
package numdiff

import "encoding/json"

func num(i int) string {
	if i < 0 {
		i = -i
	}
	switch i % 30 {
	case 0:
		return "0"
	case 1:
		return "-0"
	case 2:
		return "1"
	case 3:
		return "-1"
	case 4:
		return "1.0"
	case 5:
		return "1.50"
	case 6:
		return "0.1"
	case 7:
		return "0.000001"
	case 8:
		return "0.0000001"
	case 9:
		return "1e-6"
	case 10:
		return "1e-7"
	case 11:
		return "1e2"
	case 12:
		return "1E+2"
	case 13:
		return "12345678901234"
	case 14:
		return "123456789012345"
	case 15:
		return "1234567890123456"
	case 16:
		return "12345678901234567890"
	case 17:
		return "1e20"
	case 18:
		return "1e21"
	case 19:
		return "1.5e300"
	case 20:
		return "-2.25"
	case 21:
		return "3.141592653589793"
	case 22:
		return "0.30000000000000004"
	case 23:
		return "100"
	case 24:
		return "9007199254740993"
	case 25:
		return "5e-324"
	case 26:
		return "-0.0"
	case 27:
		return "2.5E-3"
	case 28:
		return "0.5"
	case 29:
		return "65536.125"
	}
	return "0"
}

type doc struct {
	F float64  `json:"f"`
	O float64  `json:"o,omitempty"`
	P *float64 `json:"p,omitempty"`
}

func show(b []byte, err error) string {
	if err != nil {
		return "!"
	}
	return string(b)
}

// Any decodes a document with a number in an object and an array into an
// interface and encodes it again.
func Any(i, j int) string {
	var v any
	if err := json.Unmarshal([]byte(`{"a":`+num(i)+`,"b":[`+num(j)+`,1]}`), &v); err != nil {
		return "dec!"
	}
	return show(json.Marshal(v))
}

// Field decodes numbers into float64 fields and encodes the struct again.
func Field(i, j int) string {
	var d doc
	if err := json.Unmarshal([]byte(`{"f":`+num(i)+`,"o":`+num(j)+`,"p":`+num(i)+`}`), &d); err != nil {
		return "dec!"
	}
	return show(json.Marshal(d))
}
