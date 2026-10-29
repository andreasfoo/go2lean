package semantics

import (
	"errors"
	"io"
)

// go2lean's own tests (#69): errors.Is and errors.As through error values
// of the program's own types, as sarama's KError and net.OpError are used.

type kErr int16

func (k kErr) Error() string { return "kafka error" }

const (
	kErrFenced kErr = 90
	kErrAuth   kErr = 31
)

type wrapErr struct{ inner error }

func (w wrapErr) Error() string { return "wrapped" }
func (w wrapErr) Unwrap() error { return w.inner }

type opErr struct{ op string }

func (e *opErr) Error() string { return e.op }

type isEOF struct{}

func (isEOF) Error() string        { return "is eof" }
func (isEOF) Is(target error) bool { return target == io.EOF }

func kafkaErr(fenced bool) error {
	if fenced {
		return kErrFenced
	}
	return kErrAuth
}

func testErrorsIsConst() bool {
	return errors.Is(kafkaErr(true), kErrFenced) && !errors.Is(kafkaErr(false), kErrFenced) &&
		errors.Is(kafkaErr(false), kErrAuth)
}

func testErrorsIsWrapped() bool {
	return errors.Is(wrapErr{kErrAuth}, kErrAuth) && errors.Is(wrapErr{wrapErr{io.EOF}}, io.EOF) &&
		!errors.Is(wrapErr{kErrAuth}, io.EOF) && !errors.Is(wrapErr{nil}, io.EOF)
}

func testErrorsIsMethod() bool {
	return errors.Is(isEOF{}, io.EOF) && !errors.Is(isEOF{}, kErrAuth) && errors.Is(wrapErr{isEOF{}}, io.EOF)
}

func testErrorsIsNil() bool {
	return errors.Is(nil, nil) && !errors.Is(nil, kErrAuth) && !errors.Is(kErrAuth, nil)
}

func testErrorsAsPointer() bool {
	var e error = wrapErr{&opErr{"read"}}
	target := &opErr{}
	if !errors.As(e, &target) {
		return false
	}
	return target.op == "read"
}

func testErrorsAsValue() bool {
	var k kErr
	if !errors.As(wrapErr{kErrFenced}, &k) || k != kErrFenced {
		return false
	}
	var o *opErr
	return !errors.As(wrapErr{kErrFenced}, &o) && o == nil && !errors.As(nil, &k)
}

func testErrorsAsInterface() bool {
	var u interface{ Unwrap() error }
	return errors.As(wrapErr{io.EOF}, &u) && errors.Unwrap(u.(error)) == io.EOF
}
