package semantics

import (
	"errors"
	"io"
	"strings"
)

// go2lean's own tests (#69, #47): io.ReadFull and io.ReadAll over readers
// of the program and of the standard library.

type chunkReader struct {
	data []byte
	step int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.step
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func testIOReadFullChunks() bool {
	r := &chunkReader{data: []byte("abcdef"), step: 2}
	buf := make([]byte, 5)
	n, err := io.ReadFull(r, buf)
	return n == 5 && err == nil && string(buf) == "abcde"
}

func testIOReadFullShort() bool {
	r := &chunkReader{data: []byte("abc"), step: 2}
	buf := make([]byte, 5)
	n, err := io.ReadFull(r, buf)
	return n == 3 && err == io.ErrUnexpectedEOF && errors.Is(err, io.ErrUnexpectedEOF)
}

func testIOReadFullEmpty() bool {
	r := &chunkReader{step: 2}
	n, err := io.ReadFull(r, make([]byte, 4))
	return n == 0 && err == io.EOF
}

func testIOReadFullStrings() bool {
	buf := make([]byte, 3)
	n, err := io.ReadFull(strings.NewReader("xyz!"), buf)
	return n == 3 && err == nil && string(buf) == "xyz"
}

func testIOReadAllStrings() bool {
	b, err := io.ReadAll(strings.NewReader("hello"))
	return err == nil && string(b) == "hello"
}

func testIOReadAllChunks() bool {
	b, err := io.ReadAll(&chunkReader{data: []byte("hello, world"), step: 5})
	return err == nil && string(b) == "hello, world"
}
