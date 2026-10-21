//go:build !(llvm15 || llvm16 || llvm17 || llvm18 || llvm19 || llvm20 || llvm21 || llvm22 || llvm23)

package llvm

import "errors"

// Available says whether this binary was built with the LLVM frontend.
const Available = false

var errNoLLVM = errors.New("go2lean was built without the LLVM frontend: rebuild with -tags llvm18 (or the tag of your LLVM version)")

// Gen is how the search generates one parameter.
type Gen struct {
	K      string `json:"k"`
	Name   string `json:"name"`
	W      int    `json:"w,omitempty"`
	EB     int    `json:"eb,omitempty"`
	MaxLen int    `json:"maxLen,omitempty"`
	Of     int    `json:"of"`
}

// Func is one lowered function.
type Func struct {
	Name   string
	Index  int
	Gens   []Gen
	Issues []string
}

// Model is a lowered module.
type Model struct {
	Funcs []Func
	Lean  string
}

func Compile(src, dir string) (string, error)             { return "", errNoLLVM }
func Load(path string) (*Model, error)                    { return nil, errNoLLVM }
func WriteModule(model *Model, leanDir, key string) error { return errNoLLVM }
