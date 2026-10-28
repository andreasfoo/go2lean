//go:build ignore

// gaps collects what the model lacks, as a go2lean workspace records it,
// into one ledger: each gap with the properties it affected.
//
//	go run .claude/skills/go2lean-spec/scripts/gaps.go <workspace>
//
// Sources:
//   - spurious and inconclusive verdicts: the havoc values and assumptions
//     of their counterexample, or the verdict's reason when it has none;
//   - proofs recorded as exhausted_havoc or exhausted_assuming: their
//     assumptions;
//   - hunts whose model runs got stuck;
//   - the model report's issues for each property's target function.
//
// Gaps are ordered by the number of written specs they affected, then by
// all properties (the no_panic templates included).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type property struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type target struct {
	Package string `json:"package"`
	Func    string `json:"func"`
}

type havocValue struct {
	Idx    int    `json:"idx"`
	Origin string `json:"origin"`
}

type counterexample struct {
	Property    string       `json:"property"`
	Havoc       []havocValue `json:"havoc"`
	Assumptions []string     `json:"assumptions"`
}

type verdict struct {
	Counterexample string `json:"counterexample"`
	Status         string `json:"status"`
	Diagnosis      *struct {
		Reason string `json:"reason"`
	} `json:"diagnosis"`
}

type proof struct {
	Property    string   `json:"property"`
	Status      string   `json:"status"`
	Assumptions []string `json:"assumptions"`
}

type hunt struct {
	Property string `json:"property"`
	Summary  string `json:"summary"`
}

type report struct {
	Package string `json:"package"`
	Funcs   []struct {
		Name   string   `json:"name"`
		Issues []string `json:"issues"`
	} `json:"funcs"`
	Havoc []struct {
		Site   string `json:"site"`
		Reason string `json:"reason"`
	} `json:"havoc"`
}

type gap struct {
	kind, text string
	props      map[string]bool
}

var (
	ws       string
	props    = map[string]property{}
	targets  = map[string]target{}
	reports  = map[string]*report{} // by package key (the models/gossa directory)
	byPkg    = map[string]*report{} // by import path
	gaps     = map[string]*gap{}
	fnPrefix = regexp.MustCompile(`^[\w.*()\[\]~]+: `)
	// SSA registers and the fields of one load differ between functions;
	// the gap is the same.
	register  = regexp.MustCompile(`\bt\d+\b`)
	fieldLoad = regexp.MustCompile(`\bload ([\w.]+)\.\w+$`)
	siteIndex = regexp.MustCompile(` #\d+\b`)
	stuckRe   = regexp.MustCompile(`stuck mostly at ([^;\n]+)`)
	mootRe    = regexp.MustCompile(`none in (\d+) trials \(\d+ out of fuel, (\d+) moot/stuck\)`)
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run gaps.go <workspace>")
		os.Exit(2)
	}
	ws = os.Args[1]
	each("properties", func(b []byte) {
		var p property
		if json.Unmarshal(b, &p) == nil && p.ID != "" {
			props[p.ID] = p
		}
	})
	each("targets", func(b []byte) {
		var t struct {
			ID string `json:"id"`
			target
		}
		if json.Unmarshal(b, &t) == nil && t.ID != "" {
			targets[t.ID] = t.target
		}
	})
	dirs, _ := filepath.Glob(filepath.Join(ws, "models", "gossa", "*", "report.json"))
	for _, f := range dirs {
		var r report
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &r) == nil {
			reports[filepath.Base(filepath.Dir(f))] = &r
			byPkg[r.Package] = &r
		}
	}

	each("verdicts", func(b []byte) {
		var v verdict
		if json.Unmarshal(b, &v) != nil || v.Status == "confirmed" {
			return
		}
		var cx counterexample
		if b, err := os.ReadFile(filepath.Join(ws, "counterexamples", v.Counterexample+".json")); err == nil {
			json.Unmarshal(b, &cx)
		}
		reason := ""
		if v.Diagnosis != nil {
			reason = v.Diagnosis.Reason
		}
		if v.Status == "inconclusive" {
			add("harness", "inconclusive: "+reason, cx.Property)
			return
		}
		found := false
		for _, h := range cx.Havoc {
			if s := havocSite(h, cx.Property); s != "" {
				add("havoc", s, cx.Property)
				found = true
			}
		}
		for _, a := range cx.Assumptions {
			add("assumption", strip(a), cx.Property)
			found = true
		}
		if !found {
			// Neither havoc nor an assumption explains it: the model and
			// Go disagree on modelled code, a model bug.
			add("model bug", "spurious with no havoc or assumption: "+reason, cx.Property)
		}
	})
	each("proofs", func(b []byte) {
		var p proof
		if json.Unmarshal(b, &p) != nil {
			return
		}
		switch p.Status {
		case "exhausted_assuming", "exhausted_havoc":
			for _, a := range p.Assumptions {
				add("assumption", strip(a), p.Property)
			}
			if p.Status == "exhausted_havoc" && len(p.Assumptions) == 0 {
				for _, s := range targetHavoc(p.Property) {
					add("havoc", s, p.Property)
				}
			}
		case "unknown":
			add("prover", "proof unknown", p.Property)
		}
	})
	each("hunts", func(b []byte) {
		var h hunt
		if json.Unmarshal(b, &h) != nil {
			return
		}
		if m := stuckRe.FindStringSubmatch(h.Summary); m != nil {
			add("stuck", "stuck at "+short(m[1], 120), h.Property)
		} else if m := mootRe.FindStringSubmatch(h.Summary); m != nil {
			trials, _ := strconv.Atoi(m[1])
			moot, _ := strconv.Atoi(m[2])
			if trials > 0 && moot*2 > trials {
				add("stuck", "most trials moot or stuck", h.Property)
			}
		}
	})
	for id, p := range props {
		t, ok := targets[p.Target]
		r := byPkg[t.Package]
		if !ok || r == nil {
			continue
		}
		for _, f := range r.Funcs {
			if f.Name == t.Func || strings.HasSuffix(f.Name, "."+t.Func) {
				for _, is := range f.Issues {
					add("model issue", strip(is), id)
				}
			}
		}
	}
	print()
}

// each reads every JSON file in a workspace directory.
func each(dir string, f func([]byte)) {
	files, _ := filepath.Glob(filepath.Join(ws, dir, "*.json"))
	for _, name := range files {
		if b, err := os.ReadFile(name); err == nil {
			f(b)
		}
	}
}

func add(kind, text, prop string) {
	if prop == "" {
		prop = "?"
	}
	key := kind + "\x00" + text
	g := gaps[key]
	if g == nil {
		g = &gap{kind: kind, text: text, props: map[string]bool{}}
		gaps[key] = g
	}
	g.props[prop] = true
}

// havocSite describes a havoc value by the model report's site: by its
// origin in a linked program, else by its index in the target's package.
func havocSite(h havocValue, prop string) string {
	r, idx := (*report)(nil), h.Idx
	if pkg, i, ok := strings.Cut(h.Origin, "#"); ok {
		r = reports[strings.TrimPrefix(pkg, "gossa.")]
		idx, _ = strconv.Atoi(i)
	} else if t, ok := targets[props[prop].Target]; ok {
		r = byPkg[t.Package]
	}
	if r == nil || idx < 0 || idx >= len(r.Havoc) {
		return "havoc (site not in the workspace's reports)"
	}
	s := r.Havoc[idx]
	return s.Reason + ": " + strip(s.Site)
}

// targetHavoc lists the havoc sites in a property's target function, for a
// proof recorded as exhausted_havoc.
func targetHavoc(prop string) []string {
	t, ok := targets[props[prop].Target]
	r := byPkg[t.Package]
	if !ok || r == nil {
		return []string{"havoc read (site unknown)"}
	}
	var out []string
	for _, s := range r.Havoc {
		if fn, _, ok := strings.Cut(s.Site, ": "); ok && (fn == t.Func || strings.HasSuffix(fn, "."+t.Func)) {
			out = append(out, s.Reason+": "+strip(s.Site))
		}
	}
	if len(out) == 0 {
		out = []string{"havoc read in a callee"}
	}
	return out
}

// strip drops the "Func: " prefix, so one gap met in several functions
// counts once.
func strip(s string) string {
	s = fnPrefix.ReplaceAllString(strings.TrimSpace(s), "")
	s = register.ReplaceAllString(s, "tN")
	s = fieldLoad.ReplaceAllString(s, "load $1.<field>")
	s = siteIndex.ReplaceAllString(s, "")
	return short(s, 160)
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

func print() {
	type row struct {
		g            *gap
		specs, total int
		example      string
	}
	var rows []row
	for _, g := range gaps {
		r := row{g: g, total: len(g.props)}
		var ids []string
		for id := range g.props {
			ids = append(ids, id)
			if p, ok := props[id]; ok && p.Kind != "no_panic" {
				r.specs++
			}
		}
		sort.Strings(ids)
		r.example = ids[0]
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.specs != b.specs {
			return a.specs > b.specs
		}
		if a.total != b.total {
			return a.total > b.total
		}
		return a.g.kind+a.g.text < b.g.kind+b.g.text
	})
	fmt.Printf("# Model-gap ledger: %s\n\n", ws)
	if len(rows) == 0 {
		fmt.Println("No gaps recorded.")
		return
	}
	fmt.Println("| specs | properties | kind | gap | example property |")
	fmt.Println("|---:|---:|---|---|---|")
	for _, r := range rows {
		text := strings.ReplaceAll(r.g.text, "|", `\|`)
		fmt.Printf("| %d | %d | %s | %s | %s |\n", r.specs, r.total, r.g.kind, text, r.example)
	}
}
