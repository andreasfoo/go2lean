package gossa

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDocEnumMultiLine(t *testing.T) {
	src := `package p

type Item struct {
	// The type of the item.
	//
	// Any of "message", "file_search_call",
	// "reasoning", "compaction".
	Type string
	// Any of "low", "high".
	Effort string
}
`
	file := filepath.Join(t.TempDir(), "p.go")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := pkg.Scope().Lookup("Item").Type().Underlying().(*types.Struct)
	for i, want := range [][]string{
		{"message", "file_search_call", "reasoning", "compaction"},
		{"low", "high"},
	} {
		if got := docEnum(fset, st.Field(i)); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", st.Field(i).Name(), got, want)
		}
	}
}
