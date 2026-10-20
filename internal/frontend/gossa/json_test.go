package gossa

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

const jsonSrc = `package p

import "time"

type Inner struct {
	A int ` + "`json:\"a,omitempty\"`" + `
}

type Node struct {
	Name   string            ` + "`json:\"name\"`" + `
	Skip   string            ` + "`json:\"-\"`" + `
	Dash   string            ` + "`json:\"-,\"`" + `
	hidden string
	N      int8              ` + "`json:\",omitzero\"`" + `
	Raw    []byte            ` + "`json:\"raw\"`" + `
	M      map[string]*Node  ` + "`json:\"m\"`" + `
	Next   *Node             ` + "`json:\"next,omitempty\"`" + `
	Any    any               ` + "`json:\"any\"`" + `
	Err    error             ` + "`json:\"err\"`" + `
	When   time.Time         ` + "`json:\"when\"`" + `
	Inner
}
`

func checkJSONSrc(t *testing.T) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", jsonSrc, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestJSONStructDescription(t *testing.T) {
	pkg := checkJSONSrc(t)
	j := newJSONTable(nil)
	root := j.desc(pkg.Scope().Lookup("Node").Type())
	got := j.descs[root]
	for _, want := range []string{
		"key := [110, 97, 109, 101]",            // "name"
		"key := [45]",                           // `json:"-,"` is the key "-"
		"key := [78], idx := 4",                 // N: no name in the tag, the field name
		"omitZero := true",                      // N
		"key := [110, 101, 120, 116], idx := 7", // "next"
		"omitEmpty := true",                     // next
		"inline := true",                        // the embedded Inner
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Node description lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"idx := 1,", "idx := 3,"} { // Skip, hidden
		if strings.Contains(got, unwanted) {
			t.Errorf("Node description has %q:\n%s", unwanted, got)
		}
	}
	kinds := map[string]string{}
	for k, i := range j.index {
		kinds[k] = j.descs[i]
	}
	for typ, want := range map[string]string{
		"[]byte":             ".bytes",
		"any":                ".iface",
		"error":              ".opaque",
		"time.Time":          ".opaque", // a custom MarshalJSON the model does not follow
		"*p.Node":            fmt.Sprintf("(.ptr %d)", root),
		"map[string]*p.Node": "(.map ",
		"int8":               "(.int 8 true)",
	} {
		if d, ok := kinds[typ]; !ok || !strings.HasPrefix(d, want) {
			t.Errorf("%s: description %q, want prefix %q", typ, d, want)
		}
	}
	if len(j.zeros) != len(j.descs) {
		t.Errorf("%d zero values for %d descriptions", len(j.zeros), len(j.descs))
	}
	if !strings.Contains(j.lean(nil), "descs := #[") {
		t.Errorf("table renders without descs")
	}
}
