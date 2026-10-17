Rationale: `Last` documents "last element or 0 for empty", so I state both halves directly; the loop bound `i <= len(xs)` looks suspicious at the boundary. `Clamp` should be idempotent and the identity on in-range values.

```go
package oob

// go2lean:spec Last
// SpecLastEmpty: an empty slice yields 0.
func SpecLastEmpty(xs []int, ret int) bool {
	if len(xs) != 0 {
		return true
	}
	return ret == 0
}

// go2lean:spec Last
// SpecLastIsLast: for a non-empty slice the result is its last element.
func SpecLastIsLast(xs []int, ret int) bool {
	if len(xs) == 0 {
		return true
	}
	return ret == xs[len(xs)-1]
}

// go2lean:spec Clamp
// SpecClampIdempotent: clamping a clamped value changes nothing.
func SpecClampIdempotent(v, lo, hi int, ret int) bool {
	if lo > hi {
		return true
	}
	return Clamp(ret, lo, hi) == ret
}

// go2lean:spec Clamp
// SpecClampIdentity: a value already inside [lo, hi] is returned unchanged.
func SpecClampIdentity(v, lo, hi int, ret int) bool {
	if lo > hi || v < lo || v > hi {
		return true
	}
	return ret == v
}
```
