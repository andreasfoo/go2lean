package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/andreasfoo/go2lean/internal/contract"
)

const systemPrompt = `You are the property-proposal agent of go2lean, a tool that finds bugs in Go
functions by translating them into a Lean model, searching the model for
counterexamples, and verifying each counterexample with a real Go test.

Your job: write *spec-in-Go* functions for the modelled functions of a package.
A spec is an executable oracle. go2lean compiles it into the model with the same
pipeline as the function it specifies, searches for inputs where the function's
result makes the spec return false, and then runs the real Go code on those
inputs. A spec that returns false on a real run is a confirmed bug.

Rules for a spec function:
- Signature: func Spec<Target><Suffix>(<exactly the parameters of the target, same
  names and types>, <the target's results>) bool, where a single result is
  named ret and several are named ret0, ret1, ... in order (an error result
  is "ret1 error"; test it only against nil).
- The marker line // go2lean:spec <Target> is part of the doc comment of EACH
  spec function: directly above its func line, no blank line in between. Example:

      // go2lean:spec Clamp
      // SpecClampInRange: the result lies within [lo, hi] whenever lo <= hi.
      func SpecClampInRange(v, lo, hi int, ret int) bool {
          if lo > hi {
              return true
          }
          return lo <= ret && ret <= hi
      }
- Return true when the property is vacuously satisfied (the inputs do not meet
  its precondition). Return false only when the result is wrong.
- Several specs per target are fine; give each a distinct, descriptive name.
- Stay inside the modelled subset: integers of any width, bools, slices of
  integers (read only: index and len/cap, no append/make/assignment), error
  values compared with nil, multiple results, if/for, arithmetic, comparisons,
  calls to other functions of the same package (including the target itself).
  No maps, strings, structs, pointers, closures, fmt, panics, or
  standard-library calls.
- A loop is fine; the model runs it with bounded fuel.
- The file contains spec functions only: never redefine or copy a function
  of the package. Import only the packages that the parameter and result
  types of the specs need (for example "time" for time.Duration); call
  nothing from them. Package-level variables of the target package are not
  modelled; use literals. Plain ASCII in code and comments.

Good properties are ones a real bug could violate: results inside a range,
sign and overflow behaviour on large inputs, off-by-one at boundaries
(empty slice, length 1, index == len), relations to other functions of the
package (inverses, idempotence, monotonicity), and that documented
preconditions really protect the function.

Output exactly one fenced Go code block containing the complete contents of a
file named go2lean_specs_test.go, starting with the package clause of the
target package. Outside the block, at most a few short lines of rationale.`

const reportPrompt = `You are the reporting agent of go2lean. You will get the package source, the
properties that were checked and the verdicts the pipeline produced. Write a
concise Markdown report for the package's maintainers, grounded in the source
(point at the actual line that is wrong, not a guess):

1. Confirmed bugs: for each, the property, the concrete input, what the real
   Go run did, and the one-line fix you would suggest.
2. Spurious counterexamples: which properties were wrong or too strong, and how
   you would restate them.
3. Properties that held: what the exhaustive engine certified and over what domain,
   and which ones are still only "no counterexample found".

Suggested fixes to the package belong in prose or in a Go block that contains
no "go2lean:spec" comment.

If some spec should be restated, you may also output one fenced Go code block
with a revised go2lean_specs_test.go; the pipeline will run it again. It is
recognised by its "go2lean:spec" comments. The file must follow the same
rules as before: the package clause of the target package (given below),
imports only for the types in spec signatures, spec functions only, never a
copy of a package function, plain ASCII, inside the modelled subset. Omit
the block if nothing should change.`

// goBlock matches a fenced Go block whose fences each stand on their own
// line (indentation allowed, as inside a list item). Anchoring both fences
// keeps an indented fix snippet from swallowing the spec block after it.
var goBlock = regexp.MustCompile("(?ms)^[ \\t]*```go[ \\t]*$\\n(.*?)\\n[ \\t]*```[ \\t]*$")

// extractGo returns the spec file in a response: the last fenced Go block
// that carries a go2lean:spec comment, so that fix suggestions in other Go
// blocks are not mistaken for it. Without such a block it falls back to the
// first Go block; "" when there is none.
func extractGo(resp string) string {
	blocks := goBlock.FindAllStringSubmatch(resp, -1)
	for i := len(blocks) - 1; i >= 0; i-- {
		if strings.Contains(blocks[i][1], "go2lean:spec") {
			return strings.TrimSpace(blocks[i][1]) + "\n"
		}
	}
	if len(blocks) > 0 {
		return strings.TrimSpace(blocks[0][1]) + "\n"
	}
	return ""
}

// hasSpecBlock reports whether a response carries a spec file (used in the
// report round, where a plain fix snippet must not trigger a rerun).
func hasSpecBlock(resp string) bool {
	for _, b := range goBlock.FindAllStringSubmatch(resp, -1) {
		if strings.Contains(b[1], "go2lean:spec") {
			return true
		}
	}
	return false
}

// packageSource concatenates the package's non-test Go files.
func packageSource(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		src, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "// ---- %s ----\n%s\n", n, src)
	}
	return b.String(), nil
}

func reportSummary(r *contract.ModelReport) string {
	var b strings.Builder
	for _, f := range r.Funcs {
		fmt.Fprintf(&b, "- %s: %s (%s)", f.Name, f.GoSig, f.Status)
		if f.SpecFor != "" {
			fmt.Fprintf(&b, " [spec for %s]", f.SpecFor)
		}
		b.WriteString("\n")
		for _, is := range f.Issues {
			fmt.Fprintf(&b, "    issue: %s\n", is)
		}
	}
	return b.String()
}

func proposePrompt(pkgPath, pkgName, source string, r *contract.ModelReport) string {
	return fmt.Sprintf(`Package %s (package clause: "package %s").

Source:
%s
Model report (which functions go2lean could model, and how):
%s
Write go2lean_specs_test.go for this package.`, pkgPath, pkgName, fence(source), reportSummary(r))
}

func reportUserPrompt(pkgPath, pkgName, source, verdicts string) string {
	return fmt.Sprintf(`Package %s (package clause: "package %s").

Source:
%s
%s`, pkgPath, pkgName, fence(source), verdicts)
}

var packageClause = regexp.MustCompile(`(?m)^package\s+\S+`)

// normalizeSpec forces the package clause to the target package; models
// sometimes echo a placeholder name.
func normalizeSpec(spec, pkgName string) string {
	if loc := packageClause.FindStringIndex(spec); loc != nil {
		return spec[:loc[0]] + "package " + pkgName + spec[loc[1]:]
	}
	return "package " + pkgName + "\n\n" + spec
}

func repairPrompt(issues []string, previous string) string {
	var b strings.Builder
	b.WriteString("Your previous go2lean_specs_test.go could not be fully modelled:\n")
	for _, is := range issues {
		fmt.Fprintf(&b, "- %s\n", is)
	}
	fmt.Fprintf(&b, "\nPrevious file:\n%s\nRewrite the file so that every spec stays inside the modelled subset. Drop a spec rather than keep one that cannot be modelled.", fence(previous))
	return b.String()
}

func fence(src string) string { return "```go\n" + src + "```\n" }
