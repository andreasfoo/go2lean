package gossa

import (
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
	d := j.descs[root]
	if d["k"] != "strct" {
		t.Fatalf("Node: %v", d)
	}
	byKey := map[string]map[string]any{}
	for _, f := range d["fields"].([]any) {
		m := f.(map[string]any)
		byKey[m["key"].(string)] = m
	}
	for key, want := range map[string]map[string]any{
		"name":  {"idx": 0},
		"-":     {"idx": 2},             // `json:"-,"` is the key "-"
		"N":     {"idx": 4, "oz": true}, // no name in the tag: the field name
		"next":  {"idx": 7, "oe": true},
		"Inner": {"idx": 11, "in": true}, // embedded
	} {
		f, ok := byKey[key]
		if !ok {
			t.Errorf("no field with key %q in %v", key, d)
			continue
		}
		for k, v := range want {
			if f[k] != v {
				t.Errorf("field %q: %s = %v, want %v", key, k, f[k], v)
			}
		}
	}
	for _, f := range byKey {
		if idx := f["idx"]; idx == 1 || idx == 3 {
			t.Errorf("field %v (Skip or hidden) described", f)
		}
	}
	kinds := map[string]map[string]any{}
	for k, i := range j.index {
		kinds[k] = j.descs[i]
	}
	for typ, want := range map[string]string{
		"[]byte": "bytes", "any": "iface", "error": "opaque", "time.Time": "opaque",
		"*p.Node": "ptr", "map[string]*p.Node": "map", "int8": "int",
	} {
		if kinds[typ]["k"] != want {
			t.Errorf("%s: description %v, want kind %s", typ, kinds[typ], want)
		}
	}
	if kinds["*p.Node"]["e"] != root {
		t.Errorf("*p.Node points at %v, want %d", kinds["*p.Node"]["e"], root)
	}
	if len(j.zeros) != len(j.descs) {
		t.Errorf("%d zero values for %d descriptions", len(j.zeros), len(j.descs))
	}
	lean := j.lean(nil)
	if !strings.HasPrefix(lean, "GoSSA.JTable.ofJsonString \"{") || strings.Count(lean, "\n") > 0 {
		t.Errorf("table term: %.80s", lean)
	}
}
