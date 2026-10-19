// Package contract defines the JSON records that the three go2lean modules
// exchange. M1 (programmatic) emits ABI records, M2 (agentic) emits
// Property / Counterexample / ProofResult / ModelGap, M3 (poc) emits Verdict.
//
// All integer values are carried as decimal strings so that 64-bit values
// survive JSON round trips. Rendering follows the Go type named in the ABI:
// signed decimal for signed integers, unsigned decimal for unsigned ones,
// "true"/"false" for bool.
package contract

import (
	"encoding/json"
	"strings"
)

// Target identifies one Go function and the model(s) M1 built for it.
type Target struct {
	ID      string `json:"id"`
	Module  string `json:"module"`  // Go module path
	Package string `json:"package"` // full import path of the package
	Func    string `json:"func"`
	GoSig   string `json:"go_sig"`
	// Models maps a frontend name ("gossa", "llvm") to the ABI record path,
	// relative to the workspace root.
	Models map[string]string `json:"models"`
}

// PropertyKind enumerates the property shapes M2 may propose.
type PropertyKind string

const (
	KindNoPanic       PropertyKind = "no_panic"
	KindPostcondition PropertyKind = "postcondition"
	KindEquivalence   PropertyKind = "equivalence"
	KindCrosscheck    PropertyKind = "crosscheck"
	KindCustom        PropertyKind = "custom"
)

// GoLean carries the two forms of a predicate: the Go expression M3 can
// evaluate on real values and the Lean term M2 uses on the model.
type GoLean struct {
	Go   string `json:"go,omitempty"`
	Lean string `json:"lean,omitempty"`
}

// SpecRef names a spec-in-Go function: a Go function in the target's package
// with signature func(<target params>..., ret <target result>) bool.
type SpecRef struct {
	GoFunc   string `json:"go_func"`
	LeanDecl string `json:"lean_decl,omitempty"`
}

// Property is one claim about a Target.
type Property struct {
	ID           string       `json:"id"`
	Target       string       `json:"target"`
	Kind         PropertyKind `json:"kind"`
	NL           string       `json:"nl"`
	Precondition *GoLean      `json:"precondition,omitempty"`
	Spec         *SpecRef     `json:"spec,omitempty"`
	// ExpectedPanic restricts a no_panic property to one panic category
	// (see PanicKind). Empty means any panic counts.
	ExpectedPanic PanicKind `json:"expected_panic,omitempty"`
	Frontend      string    `json:"frontend"`
	Source        string    `json:"source"` // "agent" | "human" | "template"
	CreatedBy     string    `json:"created_by,omitempty"`
}

// PanicKind is the category of a Go runtime panic as both the Lean model and
// M3 classify it.
type PanicKind string

const (
	PanicIndexOutOfRange PanicKind = "index_out_of_range"
	PanicDivideByZero    PanicKind = "divide_by_zero"
	PanicNilDeref        PanicKind = "nil_dereference"
	PanicNegativeShift   PanicKind = "negative_shift"
	PanicSliceBounds     PanicKind = "slice_bounds"
	PanicMakeSliceLen    PanicKind = "make_slice_len"
	PanicTypeAssertion   PanicKind = "type_assertion"
	PanicExplicit        PanicKind = "explicit"
	PanicOther           PanicKind = "other"
)

// HavocValue is the value the counterexample assigns to one havoc site on
// one iteration. GoHint, copied from the ABI, tells M3 how to realise it in
// Go (e.g. "xs[i]" means element Iter of parameter xs).
type HavocValue struct {
	Idx    int    `json:"idx"`
	Iter   int    `json:"iter"`
	Value  string `json:"value"`
	GoHint string `json:"go_hint,omitempty"`
}

// ModelOutput is what the Lean model returned on the counterexample.
type ModelOutput struct {
	Res   string    `json:"res"` // "ok" | "panic" | "out_of_fuel"
	Value FlexValue `json:"value,omitempty"`
	Panic PanicKind `json:"panic,omitempty"`
}

// FlexValue is a rendered model value: a single value as a string, or
// several results (a tuple) as a JSON array, read as "[v0, v1, ...]".
type FlexValue string

func (v *FlexValue) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*v = FlexValue(s)
		return nil
	}
	var parts []FlexValue
	if err := json.Unmarshal(b, &parts); err != nil {
		return err
	}
	strs := make([]string, len(parts))
	for i, p := range parts {
		strs[i] = string(p)
	}
	*v = FlexValue("[" + strings.Join(strs, ", ") + "]")
	return nil
}

// Counterexample is a concrete assignment under which the model violates a
// Property. Inputs are keyed by model parameter name (see ABIParam.Model).
type Counterexample struct {
	ID       string            `json:"id"`
	Property string            `json:"property"`
	Frontend string            `json:"frontend"`
	Engine   string            `json:"engine"` // "plausible" | "bv_decide" | "manual" | ...
	Fuel     int               `json:"fuel,omitempty"`
	Inputs   map[string]string `json:"inputs"`
	// Memory holds the slice contents the model read, keyed by Go parameter
	// name, so M3 can materialise the exact slices.
	Memory map[string][]string `json:"memory,omitempty"`
	// Objects holds struct and pointer arguments as nested JSON (a struct is
	// an array of its fields in order, a pointer is null or its pointee).
	Objects           map[string]json.RawMessage `json:"objects,omitempty"`
	Havoc             []HavocValue               `json:"havoc,omitempty"`
	ModelOutput       *ModelOutput               `json:"model_output,omitempty"`
	RevalidatedInLean bool                       `json:"revalidated_in_lean"`
}

// VerdictStatus is M3's judgement on a Counterexample.
type VerdictStatus string

const (
	VerdictConfirmed    VerdictStatus = "confirmed"
	VerdictSpurious     VerdictStatus = "spurious"
	VerdictInconclusive VerdictStatus = "inconclusive"
)

// Evidence records how M3 ran the PoC.
type Evidence struct {
	Cmd        string `json:"cmd"`
	Exit       int    `json:"exit"`
	StdoutTail string `json:"stdout_tail,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
	PanicMsg   string `json:"panic_msg,omitempty"`
}

// Diagnosis explains a spurious or inconclusive verdict so M1/M2 can act.
type Diagnosis struct {
	Reason       string   `json:"reason"`
	HavocUsed    []int    `json:"havoc_used,omitempty"`
	ModelVsReal  string   `json:"model_vs_real,omitempty"`
	IsCustomProp bool     `json:"is_custom_property,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

// Verdict is M3's output for one Counterexample.
type Verdict struct {
	Counterexample string            `json:"counterexample"`
	Status         VerdictStatus     `json:"status"`
	Evidence       Evidence          `json:"evidence"`
	Observed       map[string]string `json:"observed,omitempty"`
	Diagnosis      *Diagnosis        `json:"diagnosis,omitempty"`
	PocDir         string            `json:"poc_dir,omitempty"`
}

// ProofStatus is the outcome of a proof attempt.
type ProofStatus string

const (
	ProofProved        ProofStatus = "proved"
	ProofBoundedProved ProofStatus = "bounded_proved"
	ProofFailed        ProofStatus = "failed"
	ProofUnknown       ProofStatus = "unknown"
	// ProofExhausted: every input in a small, explicitly enumerated domain
	// satisfies the property. Weaker than bounded_proved; Domain says what
	// was covered.
	ProofExhausted ProofStatus = "exhausted"
)

// ProofResult records a proof attempt on a Property.
type ProofResult struct {
	Property string      `json:"property"`
	Status   ProofStatus `json:"status"`
	Fuel     int         `json:"fuel,omitempty"`
	Tactic   string      `json:"tactic,omitempty"`
	Domain   string      `json:"domain,omitempty"`
	LeanFile string      `json:"lean_file,omitempty"`
	TimeMs   int64       `json:"time_ms,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// ModelGap is feedback from M3/M2 to M1: a place where the model's
// over-approximation produced a spurious counterexample.
type ModelGap struct {
	FromVerdict string `json:"from_verdict"`
	Frontend    string `json:"frontend"`
	Kind        string `json:"kind"` // "unmodeled-call" | "havoc-load" | "semantic-mismatch" | ...
	Site        string `json:"site"`
	Suggestion  string `json:"suggestion,omitempty"`
}

// ABI maps the Go signature of a function onto the parameters of its model.
// Results lists every result in order; a spec-in-Go function takes them
// after the parameters, named ret for one result or ret0, ret1, ... for
// several.
type ABI struct {
	GoFunc   string `json:"go_func"`
	Package  string `json:"package,omitempty"`  // import path of the target's package (decides what the test may import)
	Variadic bool   `json:"variadic,omitempty"` // the last parameter is `...T` (its Go type is the slice)
	// Imports maps every import path a Go type mentions to the package's
	// name, which the harness needs when the two differ
	// (github.com/anthropics/anthropic-sdk-go is package anthropic).
	Imports  map[string]string `json:"imports,omitempty"`
	GoSig    string            `json:"go_sig"`
	Frontend string            `json:"frontend"`
	// Method is set for methods: the receiver is Params[0] and a call is
	// written recv.Method(rest...).
	Method string `json:"method,omitempty"`
	// Types is the table of recursive struct types referenced by TypeRef,
	// in a fixed order (the generators index it by position).
	Types   []ABIType   `json:"types,omitempty"`
	Params  []ABIParam  `json:"params"`
	Results []ABIResult `json:"results,omitempty"`
	Havoc   []ABIHavoc  `json:"havoc,omitempty"`
}

// ABIParamKind classifies how a Go parameter is split into model values.
type ABIParamKind string

const (
	ABIInt    ABIParamKind = "int"    // one model value
	ABIBool   ABIParamKind = "bool"   // one model value
	ABIError  ABIParamKind = "error"  // one model value: nil or an opaque non-nil error
	ABISlice  ABIParamKind = "slice"  // model: ref, off, len, cap (gossa) or ptr, len, cap (llvm)
	ABIStruct ABIParamKind = "struct" // one model value; Fields describe the components
	ABIPtr    ABIParamKind = "ptr"    // one model value: nil or a pointer to Pointee
	ABIString ABIParamKind = "string" // one model value; reported under Objects as a JSON string
	ABIOpaque ABIParamKind = "opaque" // interface, map, func, chan, float or unsafe.Pointer: the model never looks inside; M3 passes the zero value
	ABIArray  ABIParamKind = "array"  // one model value; Len elements of Elem/ElemParam, reported under Objects as a JSON array
)

// ABIParam describes one Go parameter. GoType is spelled as Go source in
// the target package would spell it (`time.Duration`, `Level`); TypePkg is
// the import path a test file needs for it, empty for builtins and types of
// the target package. Width/Signed come from the underlying type, so named
// integer types are handled without parsing names.
type ABIParam struct {
	Go      string       `json:"go"`
	GoType  string       `json:"go_type"`
	TypePkg string       `json:"type_pkg,omitempty"`
	Width   int          `json:"width,omitempty"`
	Signed  bool         `json:"signed,omitempty"`
	Model   []string     `json:"model"` // model parameter names, in order
	Kind    ABIParamKind `json:"kind"`
	// Slice element type, spelled like GoType, with its own package and width.
	Elem       string `json:"elem,omitempty"`
	ElemPkg    string `json:"elem_pkg,omitempty"`
	ElemWidth  int    `json:"elem_width,omitempty"`
	ElemSigned bool   `json:"elem_signed,omitempty"`
	// ElemParam describes a non-integer element type (struct, string,
	// pointer, nested slice); the contents are then reported under Objects
	// as a JSON array instead of under Memory.
	ElemParam *ABIParam `json:"elem_param,omitempty"`
	// Len is the length of an array.
	Len int `json:"len,omitempty"`
	// Zero is the Go literal of an opaque value's zero ("0" for a float,
	// "nil" otherwise), used for elements of slices and arrays.
	Zero string `json:"zero,omitempty"`
	// MaxLen, when negative, pins a string parameter to the empty string
	// (strings.Builder: the test can only pass an empty one).
	MaxLen int `json:"max_len,omitempty"`
	// ExtraPkgs are the other import paths the Go type's spelling needs
	// (a func or map type naming types of several packages).
	ExtraPkgs []string `json:"extra_pkgs,omitempty"`
	// Struct fields (Go holds the field name) and the pointee of a pointer.
	Fields  []ABIParam `json:"fields,omitempty"`
	Pointee *ABIParam  `json:"pointee,omitempty"`
	// TypeRef names an entry of ABI.Types for a struct that refers to itself
	// (directly or through pointers); Fields is then empty.
	TypeRef string `json:"type_ref,omitempty"`
}

// ABIType is a named struct type in the ABI's type table.
type ABIType struct {
	GoType  string     `json:"go_type"`
	TypePkg string     `json:"type_pkg,omitempty"`
	Fields  []ABIParam `json:"fields"`
}

// ABIResult describes the Go result.
type ABIResult struct {
	GoType  string `json:"go_type"`
	TypePkg string `json:"type_pkg,omitempty"`
	Func    bool   `json:"func,omitempty"` // a func value: the harness prints its nil-ness
	// ExtraPkgs are the other import paths the spelling needs.
	ExtraPkgs []string `json:"extra_pkgs,omitempty"`
}

// IntInfo returns the width and signedness of an integer parameter,
// falling back to the type name for records written before Width existed.
func (p ABIParam) IntInfo() (width int, signed bool, ok bool) {
	if p.Width > 0 {
		return p.Width, p.Signed, true
	}
	return GoIntType(p.GoType)
}

// ElemAsParam describes the element of a slice or array as a parameter of
// its own: ElemParam when set, else the integer element.
func (p ABIParam) ElemAsParam() ABIParam {
	if p.ElemParam != nil {
		return *p.ElemParam
	}
	return ABIParam{Go: p.Go, GoType: p.Elem, TypePkg: p.ElemPkg, Width: p.ElemWidth, Signed: p.ElemSigned, Model: []string{p.Go}, Kind: ABIInt}
}

// ElemInfo is IntInfo for a slice's element type.
func (p ABIParam) ElemInfo() (width int, signed bool, ok bool) {
	if p.ElemWidth > 0 {
		return p.ElemWidth, p.ElemSigned, true
	}
	return GoIntType(p.Elem)
}

// ABIHavoc describes one havoc site in the model.
type ABIHavoc struct {
	Idx    int    `json:"idx"`
	Site   string `json:"site"`
	Reason string `json:"reason"`
	GoHint string `json:"go_hint,omitempty"`
}

// ModelStatus says how much of a function M1 could model.
type ModelStatus string

const (
	ModelComplete ModelStatus = "complete" // every instruction modelled
	ModelPartial  ModelStatus = "partial"  // some values are havoc
	ModelRejected ModelStatus = "rejected" // not emitted
)

// ModelFunc is one function's entry in a ModelReport.
type ModelFunc struct {
	Name   string      `json:"name"`
	GoSig  string      `json:"go_sig"`
	ID     int         `json:"id"` // index into the Lean Prog.funcs array
	Status ModelStatus `json:"status"`
	Issues []string    `json:"issues,omitempty"`
	// SpecFor names the function this one is a `go2lean:spec` for.
	SpecFor string `json:"spec_for,omitempty"`
	// Consts are the integer literals in the body (two's-complement bit
	// patterns as decimal strings); the engines seed their value pools with them.
	Consts []string `json:"consts,omitempty"`
	// StrConsts are the string literals in the body, for the same purpose.
	StrConsts []string `json:"str_consts,omitempty"`
	// Calls lists the IDs of the package functions this one calls, so the
	// engines can mine the literals of the whole call tree.
	Calls []int `json:"calls,omitempty"`
	// XCalls lists the functions of other packages this one calls, when
	// those packages were compiled together with this one.
	XCalls []XCall `json:"xcalls,omitempty"`
	ABI    string  `json:"abi,omitempty"` // path of the ABI record, relative to the workspace
}

// XCall names a function of another package by its import path and its ID
// in that package's model.
type XCall struct {
	Package string `json:"package"`
	ID      int    `json:"id"`
}

// ModelReport is M1's summary of one package, read by M2 to decide what to
// attack and how.
type ModelReport struct {
	Package    string      `json:"package"`
	Frontend   string      `json:"frontend"`
	LeanModule string      `json:"lean_module"` // e.g. Go2Lean.Generated.gossa.overflow
	LeanFile   string      `json:"lean_file"`   // relative to the repo root
	Funcs      []ModelFunc `json:"funcs"`
	Havoc      []ABIHavoc  `json:"havoc,omitempty"` // program-wide havoc sites
}

// GoIntType describes a Go integer type name as the model sees it.
// ok is false for non-integer types.
func GoIntType(goType string) (width int, signed bool, ok bool) {
	switch goType {
	case "int", "int64":
		return 64, true, true
	case "uint", "uint64", "uintptr":
		return 64, false, true
	case "int32", "rune":
		return 32, true, true
	case "uint32":
		return 32, false, true
	case "int16":
		return 16, true, true
	case "uint16":
		return 16, false, true
	case "int8":
		return 8, true, true
	case "uint8", "byte":
		return 8, false, true
	}
	return 0, false, false
}
