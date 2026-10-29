package semantics

// go2lean's own tests (#55): package variables set by a call with constant
// arguments, and an alias of one, as sarama's Kafka versions are.

type kversion struct{ v [4]uint }

func newKVersion(a, b, c, d uint) kversion { return kversion{[4]uint{a, b, c, d}} }

func (x kversion) atLeast(o kversion) bool {
	for i := range x.v {
		if x.v[i] > o.v[i] {
			return true
		} else if x.v[i] < o.v[i] {
			return false
		}
	}
	return true
}

var (
	kv0820    = newKVersion(0, 8, 2, 0)
	kv0110    = newKVersion(0, 11, 0, 0)
	kvMinimum = kv0820
)

func testInitCallVersions() bool {
	return !kvMinimum.atLeast(kv0110) && kv0110.atLeast(kvMinimum) && kv0110.atLeast(kv0110)
}
