package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Workspace is a directory tree holding one JSON file per record:
//
//	<root>/targets/<id>.json
//	<root>/properties/<id>.json
//	<root>/counterexamples/<id>.json
//	<root>/verdicts/<id>.json        (keyed by counterexample id)
//	<root>/proofs/<id>.json          (keyed by property id)
//	<root>/gaps/<verdict id>-<n>.json
//	<root>/poc/<counterexample id>/  (M3 deliverables)
//
// ABI records live wherever Target.Models points, relative to the root.
type Workspace struct {
	Root string
}

// Init creates the workspace root and marks it as a separate Go module, so
// that the PoC tests it holds are not picked up by `go vet ./...` or
// `go test ./...` in the enclosing repository.
func (w Workspace) Init() error {
	if err := os.MkdirAll(w.Root, 0o755); err != nil {
		return err
	}
	gomod := filepath.Join(w.Root, "go.mod")
	if _, err := os.Stat(gomod); err == nil {
		return nil
	}
	return os.WriteFile(gomod, []byte("// go2lean workspace: keeps PoC tests out of the enclosing module.\nmodule go2lean.invalid/workspace\n"), 0o644)
}

func (w Workspace) path(kind, id string) string {
	return filepath.Join(w.Root, kind, id+".json")
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (w Workspace) Target(id string) (*Target, error) {
	var t Target
	if err := readJSON(w.path("targets", id), &t); err != nil {
		return nil, err
	}
	return &t, t.Validate()
}

func (w Workspace) Property(id string) (*Property, error) {
	var p Property
	if err := readJSON(w.path("properties", id), &p); err != nil {
		return nil, err
	}
	return &p, p.Validate()
}

func (w Workspace) Counterexample(id string) (*Counterexample, error) {
	var c Counterexample
	if err := readJSON(w.path("counterexamples", id), &c); err != nil {
		return nil, err
	}
	return &c, c.Validate()
}

// ABI loads the ABI record a Target names for a frontend.
func (w Workspace) ABI(t *Target, frontend string) (*ABI, error) {
	rel, ok := t.Models[frontend]
	if !ok {
		return nil, fmt.Errorf("target %s has no model for frontend %q", t.ID, frontend)
	}
	var a ABI
	if err := readJSON(filepath.Join(w.Root, rel), &a); err != nil {
		return nil, err
	}
	return &a, a.Validate()
}

// HuntRecord says that a property was searched and what came of it, so a
// resumed run can skip it.
type HuntRecord struct {
	Property       string `json:"property"`
	Summary        string `json:"summary"`
	Counterexample string `json:"counterexample,omitempty"`
}

func (w Workspace) WriteHuntRecord(r *HuntRecord) error {
	return writeJSON(w.path("hunts", r.Property), r)
}

// HuntRecord returns the record of a property's search, or nil.
func (w Workspace) HuntRecord(propID string) *HuntRecord {
	var r HuntRecord
	if readJSON(w.path("hunts", propID), &r) != nil {
		return nil
	}
	return &r
}

// Verdict returns the verdict of a counterexample, or nil.
func (w Workspace) Verdict(cxID string) *Verdict {
	var v Verdict
	if readJSON(w.path("verdicts", cxID), &v) != nil {
		return nil
	}
	return &v
}

func (w Workspace) WriteVerdict(v *Verdict) error {
	return writeJSON(w.path("verdicts", v.Counterexample), v)
}

func (w Workspace) WriteProperty(p *Property) error {
	return writeJSON(w.path("properties", p.ID), p)
}

func (w Workspace) WriteCounterexample(c *Counterexample) error {
	return writeJSON(w.path("counterexamples", c.ID), c)
}

func (w Workspace) WriteProofResult(r *ProofResult) error {
	return writeJSON(w.path("proofs", r.Property), r)
}

// PocDir is where M3 puts the deliverables for one counterexample.
func (w Workspace) PocDir(cxID string) string {
	return filepath.Join(w.Root, "poc", cxID)
}

func (w Workspace) WriteTarget(t *Target) error {
	return writeJSON(w.path("targets", t.ID), t)
}

// PackageKey is the workspace directory name for a package: the full import
// path with anything that is not an identifier character replaced, so that
// packages sharing a base name (util, types, ...) never collide.
func PackageKey(pkgPath string) string {
	return nonIdent.ReplaceAllString(pkgPath, "_")
}

var nonIdent = regexp.MustCompile(`[^A-Za-z0-9_]`)

// WriteABI stores an ABI record under models/<frontend>/<pkg>/<func>.json
// and returns the workspace-relative path for Target.Models.
func (w Workspace) WriteABI(a *ABI, pkgPath, funcName string) (string, error) {
	// Instances of generic functions carry their type arguments, import
	// paths included, in the name: keep them out of the directory layout.
	file := strings.NewReplacer("/", "_", "*", "_").Replace(funcName)
	rel := filepath.Join("models", a.Frontend, PackageKey(pkgPath), file+".json")
	return rel, writeJSON(filepath.Join(w.Root, rel), a)
}

func (w Workspace) WriteModelReport(r *ModelReport) error {
	return writeJSON(filepath.Join(w.Root, "models", r.Frontend, PackageKey(r.Package), "report.json"), r)
}

func (w Workspace) ModelReport(frontend, pkgPath string) (*ModelReport, error) {
	var r ModelReport
	err := readJSON(filepath.Join(w.Root, "models", frontend, PackageKey(pkgPath), "report.json"), &r)
	return &r, err
}

// ModelReportByKey reads a package's report by its package key (the
// directory name under models/<frontend>/).
func (w Workspace) ModelReportByKey(frontend, key string) (*ModelReport, error) {
	var r ModelReport
	err := readJSON(filepath.Join(w.Root, "models", frontend, key, "report.json"), &r)
	return &r, err
}

// TargetID is the workspace id of a function: package key and function
// name (a method is "Type.Method").
func TargetID(pkgPath, funcName string) string {
	return "t_" + strings.ToLower(nonIdent.ReplaceAllString(PackageKey(pkgPath)+"_"+funcName, "_"))
}

// Properties lists every property in the workspace.
func (w Workspace) Properties() ([]*Property, error) {
	matches, _ := filepath.Glob(filepath.Join(w.Root, "properties", "*.json"))
	var out []*Property
	for _, m := range matches {
		var p Property
		if err := readJSON(m, &p); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, nil
}
