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
- Signature: func Spec<Name>(<exactly the parameters of the target, same names and
  types>, ret <the target's result type>) bool
- Doc comment line: // go2lean:spec <Target>
- Return true when the property is vacuously satisfied (the inputs do not meet
  its precondition). Return false only when the result is wrong.
- Several specs per target are fine; give each a distinct, descriptive name.
- Stay inside the modelled subset: integers of any width, bools, slices of
  integers (read only: index and len/cap, no append/make/assignment), if/for,
  arithmetic, comparisons, calls to other functions of the same package
  (including the target itself). No maps, strings, structs, pointers, closures,
  multiple results, fmt, panics, or standard-library calls.
- A loop is fine; the model runs it with bounded fuel.

Good properties are ones a real bug could violate: results inside a range,
sign and overflow behaviour on large inputs, off-by-one at boundaries
(empty slice, length 1, index == len), relations to other functions of the
package (inverses, idempotence, monotonicity), and that documented
preconditions really protect the function.

Output exactly one fenced Go code block containing the complete contents of a
file named go2lean_specs_test.go, starting with the package clause of the
target package. Outside the block, at most a few short lines of rationale.`

const reportPrompt = `You are the reporting agent of go2lean. Below are the properties that were
checked for a package and the verdicts the pipeline produced. Write a concise
Markdown report for the package's maintainers:

1. Confirmed bugs: for each, the property, the concrete input, what the real
   Go run did, and the one-line fix you would suggest.
2. Spurious counterexamples: which properties were wrong or too strong, and how
   you would restate them.
3. Properties that held: what the exhaustive engine certified and over what domain,
   and which ones are still only "no counterexample found".

If some spec should be restated, you may also output one fenced Go code block
with a revised go2lean_specs_test.go; the pipeline will run it again. Omit the
block if nothing should change.`

var goBlock = regexp.MustCompile("(?s)```go\\s*\\n(.*?)\\n```")

// extractGo returns the first fenced Go block in a response, or "".
func extractGo(resp string) string {
	if m := goBlock.FindStringSubmatch(resp); m != nil {
		return strings.TrimSpace(m[1]) + "\n"
	}
	return ""
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
