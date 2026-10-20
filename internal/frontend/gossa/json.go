package gossa

import (
	"fmt"
	"go/constant"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/andreasfoo/go2lean/internal/ir"
)

// jsonTable collects the JSON descriptions (Go2Lean.GoSSA.JDesc) of the
// types a package encodes or decodes with encoding/json, and of the types
// its interfaces may hold. The Lean module gets it as `jsonTable`; the
// jsonMarshal / jsonDecode ops refer to entries by index.
type jsonTable struct {
	descs []string // Lean JDesc terms
	zeros []string // Lean Val terms: the zero value of each entry's type
	index map[string]int
	sprog *ssa.Program
	// keys are the JSON keys of the struct fields described: decoding
	// inputs need them.
	keys []string
}

func newJSONTable(sprog *ssa.Program) *jsonTable {
	return &jsonTable{index: map[string]int{}, sprog: sprog}
}

// leanBytes spells a string as the List Nat of its bytes.
func leanBytes(s string) string {
	parts := make([]string, len(s))
	for i := 0; i < len(s); i++ {
		parts[i] = fmt.Sprint(s[i])
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// sdkPkg says whether path is (inside) the OpenAI or Anthropic Go SDK,
// whose types encode by their own rules (param objects, unions, Opt,
// constants) and decode with JSON metadata.
func sdkPkg(path string) bool {
	return strings.HasPrefix(path, "github.com/openai/openai-go") || strings.HasPrefix(path, "github.com/anthropics/anthropic-sdk-go")
}

func namedIn(t types.Type, pkgSuffix, name string) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && strings.HasSuffix(n.Obj().Pkg().Path(), pkgSuffix) && n.Obj().Name() == name
}

func hasMethod(t types.Type, name string) bool {
	for _, mt := range []types.Type{t, types.NewPointer(t)} {
		ms := types.NewMethodSet(mt)
		for i := 0; i < ms.Len(); i++ {
			if ms.At(i).Obj().Name() == name {
				return true
			}
		}
	}
	return false
}

// constDefault is what an SDK constant type's Default method returns
// (`func (c Text) Default() Text { return "text" }`), if it is a literal.
func (j *jsonTable) constDefault(t types.Type) string {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || j.sprog == nil {
		return ""
	}
	for i := 0; i < n.NumMethods(); i++ {
		m := n.Method(i)
		if m.Name() != "Default" {
			continue
		}
		fn := j.sprog.FuncValue(m)
		if fn == nil {
			return ""
		}
		for _, b := range fn.Blocks {
			for _, in := range b.Instrs {
				if r, ok := in.(*ssa.Return); ok && len(r.Results) == 1 {
					if c, ok := r.Results[0].(*ssa.Const); ok && c.Value != nil && c.Value.Kind() == constant.String {
						return constant.StringVal(c.Value)
					}
				}
			}
		}
	}
	return ""
}

// desc returns the table index of t's description, building it (and the
// descriptions it refers to) on first use.
func (j *jsonTable) desc(t types.Type) int {
	key := types.TypeString(t, nil)
	if i, ok := j.index[key]; ok {
		return i
	}
	i := len(j.descs)
	j.index[key] = i
	j.descs = append(j.descs, ".opaque")
	zero := ".unit"
	if z, ok := zeroVal(t); ok {
		zero = z.Lean()
	} else if isNilable(t) {
		zero = ir.OpaqueNil{}.Lean()
	}
	j.zeros = append(j.zeros, zero)
	j.descs[i] = j.build(t)
	return i
}

func (j *jsonTable) build(t types.Type) string {
	t = types.Unalias(t)
	switch {
	case namedIn(t, "/packages/param", "Opt"):
		return fmt.Sprintf("(.opt %d)", j.desc(t.(*types.Named).TypeArgs().At(0)))
	case namedIn(t, "encoding/json", "RawMessage"):
		return ".raw"
	}
	n, named := t.(*types.Named)
	inSDK := named && n.Obj().Pkg() != nil && sdkPkg(n.Obj().Pkg().Path())
	if st, ok := t.Underlying().(*types.Struct); ok && inSDK && hasMethod(t, "MarshalJSON") && isUnionStruct(st) {
		var vs []string
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if f.Exported() && strings.HasPrefix(f.Name(), "Of") {
				vs = append(vs, fmt.Sprintf("{ key := [], idx := %d, ty := %d }", i, j.desc(f.Type())))
			}
		}
		return "(.union [" + strings.Join(vs, ", ") + "])"
	}
	if isString(t) && inSDK && hasMethod(t, "Default") {
		return fmt.Sprintf("(.str %s)", leanBytes(j.constDefault(t)))
	}
	if !inSDK && (hasMethod(t, "MarshalJSON") || hasMethod(t, "UnmarshalJSON")) {
		return ".opaque" // a custom encoding the model does not follow
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsString != 0:
			return "(.str [])"
		case u.Info()&types.IsBoolean != 0:
			return ".bool"
		case u.Info()&types.IsInteger != 0:
			w, signed, _ := intWidth(t)
			return fmt.Sprintf("(.int %d %v)", w, signed)
		case u.Info()&types.IsFloat != 0:
			return ".float"
		}
	case *types.Slice:
		if isByteSlice(t) {
			return ".bytes"
		}
		return fmt.Sprintf("(.slice %d)", j.desc(u.Elem()))
	case *types.Array:
		return fmt.Sprintf("(.arr %d %d)", u.Len(), j.desc(u.Elem()))
	case *types.Pointer:
		return fmt.Sprintf("(.ptr %d)", j.desc(u.Elem()))
	case *types.Map:
		if _, _, isInt := intWidth(u.Key()); isString(u.Key()) || isInt {
			return fmt.Sprintf("(.map %d %d)", j.desc(u.Key()), j.desc(u.Elem()))
		}
	case *types.Interface:
		if !isError(t) {
			return ".iface"
		}
	case *types.Struct:
		return j.structDesc(u)
	}
	return ".opaque"
}

// isUnionStruct says whether st is an SDK union: its exported fields are
// all variants `Of<Variant>`.
func isUnionStruct(st *types.Struct) bool {
	seen := false
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Exported() {
			continue
		}
		if !strings.HasPrefix(f.Name(), "Of") {
			return false
		}
		seen = true
	}
	return seen
}

func (j *jsonTable) structDesc(st *types.Struct) string {
	var fields []string
	meta := "none"
	jsonKeys := map[string]string{} // Go field name -> JSON key
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		optList := strings.Split(opts, ",")
		if f.Name() == "JSON" && name == "-" {
			if mst, ok := f.Type().Underlying().(*types.Struct); ok && isRespJSONMeta(mst) {
				meta = j.metaDesc(i, mst, jsonKeys)
			}
			continue
		}
		if tag == "-" {
			continue // `json:"-,"` is the key "-"
		}
		inline := (f.Anonymous() && name == "") || (name == "" && contains(optList, "inline"))
		if !f.Exported() && !inline {
			continue
		}
		if !f.Exported() && f.Anonymous() {
			// An unexported embedded struct (the SDKs' paramObj) holds
			// metadata, not members.
			continue
		}
		key := name
		if key == "" {
			key = f.Name()
		}
		jsonKeys[f.Name()] = key
		if !contains(j.keys, key) {
			j.keys = append(j.keys, key)
		}
		fields = append(fields, fmt.Sprintf("{ key := %s, idx := %d, ty := %d, omitEmpty := %v, omitZero := %v, inline := %v }",
			leanBytes(key), i, j.desc(f.Type()), contains(optList, "omitempty"), contains(optList, "omitzero"), inline))
	}
	return "(.strct [" + strings.Join(fields, ", ") + "] " + meta + ")"
}

// metaDesc describes the JSON metadata field (index fi) of an SDK response
// type: where its raw text, ExtraFields and per-member respjson.Field go.
// It is called after the typed fields before it were seen; members are
// matched by Go field name.
func (j *jsonTable) metaDesc(fi int, mst *types.Struct, jsonKeys map[string]string) string {
	raw, extra := -1, "none"
	var members []string
	for k := 0; k < mst.NumFields(); k++ {
		f := mst.Field(k)
		switch {
		case f.Name() == "raw":
			raw = k
		case f.Name() == "ExtraFields":
			extra = fmt.Sprintf("(some %d)", k)
		default:
			if key, ok := jsonKeys[f.Name()]; ok {
				members = append(members, fmt.Sprintf("(%s, %d)", leanBytes(key), k))
			}
		}
	}
	if raw < 0 {
		return "none"
	}
	return fmt.Sprintf("(some { field := %d, raw := %d, extra := %s, members := [%s] })", fi, raw, extra, strings.Join(members, ", "))
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// lean renders the table, with the descriptions of the given boxed types
// for encoding interface values, or "" when no JSON op uses it.
func (j *jsonTable) lean(boxed []types.Type) string {
	if len(j.descs) == 0 {
		return ""
	}
	var bx []string
	for _, t := range boxed {
		bx = append(bx, fmt.Sprintf("(%d, %d)", typeTag(t), j.desc(t)))
	}
	empty := types.NewInterfaceType(nil, nil)
	basic := []types.Type{types.Typ[types.String], types.Typ[types.Bool], types.Typ[types.Float64],
		types.NewMap(types.Typ[types.String], empty), types.NewSlice(empty)}
	var bs []string
	for _, t := range basic {
		bs = append(bs, fmt.Sprint(typeTag(t)))
	}
	return fmt.Sprintf("{ descs := #[%s],\n    zeros := #[%s],\n    boxed := [%s],\n    basic := [%s] }",
		strings.Join(j.descs, ",\n      "), strings.Join(j.zeros, ", "), strings.Join(bx, ", "), strings.Join(bs, ", "))
}
