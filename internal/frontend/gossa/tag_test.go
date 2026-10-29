package gossa

import (
	"go/token"
	"go/types"
	"testing"
)

// A type alias is the type it stands for at run time, also inside a
// pointer, slice or map: it gets the same tag (#60).
func TestTypeTagSeesThroughAliases(t *testing.T) {
	pkg := types.NewPackage("example.com/m", "m")
	named := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "DefinedStr", nil), types.Typ[types.String], nil)
	alias := types.NewAlias(types.NewTypeName(token.NoPos, pkg, "DefinedStr2", nil), named)
	for _, c := range [][2]types.Type{
		{named, alias},
		{types.NewPointer(named), types.NewPointer(alias)},
		{types.NewSlice(named), types.NewSlice(alias)},
		{types.NewMap(types.Typ[types.String], named), types.NewMap(types.Typ[types.String], alias)},
	} {
		if typeTag(c[0]) != typeTag(c[1]) {
			t.Errorf("%s and %s get different tags", c[0], c[1])
		}
	}
	if typeTag(types.NewPointer(named)) == typeTag(types.NewPointer(types.Typ[types.String])) {
		t.Error("*DefinedStr and *string get the same tag")
	}
}
