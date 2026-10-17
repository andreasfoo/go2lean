// Package checked is a ground-truth example for go2lean: functions with
// (T, error) results. Double reports overflow through its error result but
// checks the wrong condition, so some overflowing inputs come back as
// success with a wrong value.
package checked

import "errors"

var ErrOverflow = errors.New("overflow")

// Double returns 2*n, or ErrOverflow when the product does not fit. The
// guard only covers positive n, so large negative n overflows silently.
func Double(n int) (int, error) {
	if n > 4611686018427387903 {
		return 0, ErrOverflow
	}
	return n * 2, nil
}

// Half returns n/2 and never fails; it is correct.
func Half(n int) (int, error) {
	return n / 2, nil
}

// SpecDoubleHalves: on success, halving the result gives n back.
//
// go2lean:spec Double
func SpecDoubleHalves(n int, ret0 int, ret1 error) bool {
	if ret1 != nil {
		return true
	}
	return ret0/2 == n
}

// SpecHalfNoError: Half never reports an error.
//
// go2lean:spec Half
func SpecHalfNoError(n int, ret0 int, ret1 error) bool {
	return ret1 == nil
}
