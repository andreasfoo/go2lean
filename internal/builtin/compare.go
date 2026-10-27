// Package builtin constructs tool-owned semantic oracles. Binding establishes
// type compatibility only, not a function's contract or model completeness.
package builtin

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/tools/go/packages"
)

const (
	CompareOrderID      = "compare-order"
	CompareOrderVersion = "1"
	CompareOrderFile    = "zz_go2lean_builtin_compare_order.go"
	CompareOrderTarget  = "go2leanBuiltinCompareOrderV1"
	CompareOrderOracle  = "go2leanSpecCompareOrderV1"
	CompareOrderDomain  = "all Go byte strings"
	CompareOrderLaw     = "reflexive zero; reversed signs; transitive nonpositive comparison"
)

// Binding is reproducible generated-source provenance, not an enabled-check or
// passed-check record. Model capability, search, replay and artifact delivery
// must be established separately before using this as release evidence.
type Binding struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Stage       string   `json:"stage"`
	Package     string   `json:"package"`
	Function    string   `json:"function"`
	Domain      string   `json:"domain"`
	Law         string   `json:"law"`
	Assumptions []string `json:"assumptions"`
	Target      string   `json:"target"`
	Oracle      string   `json:"oracle"`
	File        string   `json:"file"`
	SHA256      string   `json:"sha256"`
	Source      string   `json:"source"`
}

// Overlay returns a fresh source buffer so a caller cannot mutate the binding.
func (b *Binding) Overlay() map[string][]byte {
	return map[string][]byte{b.File: []byte(b.Source)}
}

func compareOrderAssumptions() []string {
	return []string{
		"explicit user selection, not inferred from a name or signature",
		"pure, deterministic and total on every string",
		"negative/zero/positive denotes less/equivalent/greater in a strict weak ordering",
		"no equality of input bytes is required for comparator equivalence",
	}
}

// Validate checks this tool's supported property card and canonical generated
// source, including its package/function binding. A matching hash alone does
// not establish that arbitrary Go code implements this builtin.
func (b *Binding) Validate() error {
	if b == nil || b.ID != CompareOrderID || b.Version != CompareOrderVersion ||
		b.Stage != "type-compatible-source-only" || b.Target != CompareOrderTarget ||
		b.Oracle != CompareOrderOracle || b.Domain != CompareOrderDomain ||
		b.Law != CompareOrderLaw || !slices.Equal(b.Assumptions, compareOrderAssumptions()) {
		return fmt.Errorf("compare-order: unsupported or inconsistent property card")
	}
	if b.Package == "" || !token.IsIdentifier(b.Function) || b.Function == "_" ||
		!filepath.IsAbs(b.File) || filepath.Base(b.File) != CompareOrderFile || b.Source == "" ||
		b.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(b.Source))) {
		return fmt.Errorf("compare-order: invalid source provenance")
	}
	file, err := parser.ParseFile(token.NewFileSet(), b.File, b.Source, 0)
	if err != nil {
		return fmt.Errorf("compare-order: invalid generated source: %w", err)
	}
	canonical, err := compareOrderSource(file.Name.Name, b.Package, b.Function)
	if err != nil || !bytes.Equal(canonical, []byte(b.Source)) {
		return fmt.Errorf("compare-order: source is not the canonical bound oracle")
	}
	return nil
}

// BindCompareOrder requires explicit selection of a package-level comparator
// and exactly one package. No name/signature heuristic activates the law.
// The caller must assert that this is a pure, deterministic, total three-way
// strict-weak-order comparator over ALL strings. Restricted domains, methods,
// function variables, generics and named string/int types are not supported.
//
// This loads trusted Go source with the ordinary Go tooling; it is not a
// sandbox and does not execute the selected function.
func BindCompareOrder(dir, selected string, overlay map[string][]byte, patterns ...string) (*Binding, error) {
	if !token.IsIdentifier(selected) || token.Lookup(selected).IsKeyword() || selected == "_" {
		return nil, fmt.Errorf("compare-order: explicitly select a package-level function identifier")
	}
	if len(patterns) == 0 {
		return nil, fmt.Errorf("compare-order: select exactly one package")
	}
	pkgs, err := packages.Load(&packages.Config{
		Dir: dir, Overlay: overlay,
		// Source type-checking is necessary: export data can omit unused
		// unexported declarations and would miss reserved-name collisions.
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedTypes |
			packages.NeedImports | packages.NeedSyntax | packages.NeedTypesInfo,
	}, patterns...)
	if err != nil {
		return nil, fmt.Errorf("compare-order: load package: %w", err)
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("compare-order: expected one package, got %d", len(pkgs))
	}
	p := pkgs[0]
	if len(p.Errors) != 0 {
		return nil, fmt.Errorf("compare-order: package load failed: %s", p.Errors[0])
	}
	if p.Types == nil || len(p.GoFiles) == 0 {
		return nil, fmt.Errorf("compare-order: package has no usable Go files/types")
	}
	fn, ok := p.Types.Scope().Lookup(selected).(*types.Func)
	if !ok {
		return nil, fmt.Errorf("compare-order: %s is not a package-level function", selected)
	}
	sig := fn.Type().(*types.Signature)
	if sig.Recv() != nil || sig.TypeParams().Len() != 0 || sig.Variadic() ||
		sig.Params().Len() != 2 || sig.Results().Len() != 1 ||
		!types.Identical(sig.Params().At(0).Type(), types.Typ[types.String]) ||
		!types.Identical(sig.Params().At(1).Type(), types.Typ[types.String]) ||
		!types.Identical(sig.Results().At(0).Type(), types.Typ[types.Int]) {
		return nil, fmt.Errorf("compare-order: %s must have exact signature func(string, string) int", selected)
	}
	for _, name := range []string{CompareOrderTarget, CompareOrderOracle} {
		if p.Types.Scope().Lookup(name) != nil {
			return nil, fmt.Errorf("compare-order: generated identifier %s already exists", name)
		}
	}
	path := filepath.Join(filepath.Dir(p.GoFiles[0]), CompareOrderFile)
	if _, exists := overlay[path]; exists {
		return nil, fmt.Errorf("compare-order: generated file already exists in overlay: %s", path)
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("compare-order: generated file already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("compare-order: inspect generated path: %w", err)
	}
	src, err := compareOrderSource(p.Name, p.PkgPath, selected)
	if err != nil {
		return nil, fmt.Errorf("compare-order: generate source: %w", err)
	}
	return &Binding{
		ID: CompareOrderID, Version: CompareOrderVersion, Stage: "type-compatible-source-only",
		Package: p.PkgPath, Function: selected, Domain: CompareOrderDomain,
		Law: CompareOrderLaw, Assumptions: compareOrderAssumptions(),
		Target: CompareOrderTarget, Oracle: CompareOrderOracle,
		File: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(src)), Source: string(src),
	}, nil
}

func compareOrderSource(packageName, packagePath, selected string) ([]byte, error) {
	return format.Source([]byte(fmt.Sprintf(`// Code generated by go2lean compare-order v1. DO NOT EDIT.
// Binding: %s.%s
// Explicit opt-in: %s is assumed pure, deterministic, total over all strings,
// and intended to supply a three-way strict weak ordering.
package %s

func %s(a, b, c string) (aa, ab, ba, bc, ac int) {
	return %s(a, a), %s(a, b), %s(b, a), %s(b, c), %s(a, c)
}

// go2lean:spec %s
func %s(a, b, c string, aa, ab, ba, bc, ac int) bool {
	// Compare signs, never magnitudes: negating MinInt would overflow.
	reflexive := aa == 0
	reversed := (ab < 0 && ba > 0) || (ab > 0 && ba < 0) || (ab == 0 && ba == 0)
	// Transitivity of <= includes transitivity of the zero-equivalence
	// relation; with sign reversal it checks a three-way weak ordering.
	transitive := !(ab <= 0 && bc <= 0) || ac <= 0
	return reflexive && reversed && transitive
}
`, packagePath, selected, selected, packageName, CompareOrderTarget, selected, selected, selected, selected, selected,
		CompareOrderTarget, CompareOrderOracle)))
}
