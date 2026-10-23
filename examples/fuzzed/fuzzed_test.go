package fuzzed

import "testing"

// FuzzRoundTrip finds the planted bug: Decode(Encode(n)) != n for an n
// with an f digit.
func FuzzRoundTrip(f *testing.F) {
	f.Add(uint16(1))
	f.Fuzz(func(t *testing.T, n uint16) {
		s := Encode(n)
		m, ok := Decode(s)
		if !ok {
			t.Fatalf("Decode(%q) failed", s)
		}
		if m != n {
			t.Errorf("Decode(Encode(%d)) = %d", n, m)
		}
	})
}

// FuzzDecodeShort holds: Decode accepts at most four digits.
func FuzzDecodeShort(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) <= 4 {
			t.Skip()
		}
		if _, ok := Decode(s); ok {
			t.Fatal("accepted more than four digits")
		}
	})
}

// FuzzWithHelper is not read: it hands t to a helper.
func FuzzWithHelper(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) {
		check(t, s)
	})
}

func check(t *testing.T, s string) {
	if n, ok := Decode(s); ok && Encode(n) == "" {
		t.Fatal("empty encoding")
	}
}
