package gossa

import (
	"os"
	"path/filepath"
	"testing"
)

// errors.Is, As and Unwrap compile when the only error types with Is and
// Unwrap methods are ones the model does not translate (syscall.Errno,
// *fs.PathError): the walk then has no method to call, and every block it
// makes still ends in a terminator (#69; a dangling block made evaluation
// 7's k004 fail to compile).
func TestErrorsWalkWithoutTranslatedMethods(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.22\n",
		"m.go": `package m

import (
	"errors"
	"io/fs"
	"syscall"
)

var errno error = syscall.ENOENT

var pathErr error = &fs.PathError{Op: "open", Path: "x", Err: errno}

func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

func Inner(err error) error { return errors.Unwrap(err) }

func AsPath(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe)
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Translate(dir, "."); err != nil {
		t.Fatal(err)
	}
}
