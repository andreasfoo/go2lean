package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andreasfoo/go2lean/internal/contract"
)

// Summary aggregates a workspace: every verdict grouped by status and every
// proof result, so a sweep over many packages can be read in one place.
type Summary struct {
	Confirmed    []Finding
	Spurious     []Finding
	Inconclusive []Finding
	Proofs       map[contract.ProofStatus]int
	Properties   int
}

// Finding is one verdict with the records it points at.
type Finding struct {
	Verdict        *contract.Verdict
	Counterexample *contract.Counterexample
	Property       *contract.Property
}

func readAll[T any](dir string) ([]T, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(matches)
	var out []T
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", m, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// Summarize reads a workspace.
func Summarize(w contract.Workspace) (*Summary, error) {
	verdicts, err := readAll[contract.Verdict](filepath.Join(w.Root, "verdicts"))
	if err != nil {
		return nil, err
	}
	proofs, err := readAll[contract.ProofResult](filepath.Join(w.Root, "proofs"))
	if err != nil {
		return nil, err
	}
	props, err := w.Properties()
	if err != nil {
		return nil, err
	}
	s := &Summary{Proofs: map[contract.ProofStatus]int{}, Properties: len(props)}
	for _, p := range proofs {
		s.Proofs[p.Status]++
	}
	for i := range verdicts {
		v := &verdicts[i]
		f := Finding{Verdict: v}
		if cx, err := w.Counterexample(v.Counterexample); err == nil {
			f.Counterexample = cx
			if p, err := w.Property(cx.Property); err == nil {
				f.Property = p
			}
		}
		switch v.Status {
		case contract.VerdictConfirmed:
			s.Confirmed = append(s.Confirmed, f)
		case contract.VerdictSpurious:
			s.Spurious = append(s.Spurious, f)
		default:
			s.Inconclusive = append(s.Inconclusive, f)
		}
	}
	return s, nil
}

// WriteMarkdown renders the summary.
func (s *Summary) WriteMarkdown(out io.Writer) {
	fmt.Fprintf(out, "# go2lean summary\n\n%d properties; %d confirmed, %d spurious, %d inconclusive; proofs: %v\n\n",
		s.Properties, len(s.Confirmed), len(s.Spurious), len(s.Inconclusive), s.Proofs)
	section := func(title string, fs []Finding) {
		if len(fs) == 0 {
			return
		}
		fmt.Fprintf(out, "## %s (%d)\n\n", title, len(fs))
		for _, f := range fs {
			fmt.Fprintf(out, "### %s\n\n", f.Verdict.Counterexample)
			if f.Property != nil {
				fmt.Fprintf(out, "- property: `%s` (%s) — %s\n", f.Property.ID, f.Property.Kind, f.Property.NL)
			}
			if cx := f.Counterexample; cx != nil {
				fmt.Fprintf(out, "- inputs: `%v`", cx.Inputs)
				if len(cx.Memory) > 0 {
					fmt.Fprintf(out, " memory: `%v`", cx.Memory)
				}
				if len(cx.Objects) > 0 {
					var parts []string
					for k, v := range cx.Objects {
						parts = append(parts, k+"="+string(v))
					}
					sort.Strings(parts)
					fmt.Fprintf(out, " objects: `%s`", strings.Join(parts, " "))
				}
				if cx.ModelOutput != nil {
					fmt.Fprintf(out, "\n- model: %s %s %s", cx.ModelOutput.Res, cx.ModelOutput.Value, cx.ModelOutput.Panic)
				}
				fmt.Fprintln(out)
			}
			fmt.Fprintf(out, "- real: `%v`", f.Verdict.Observed)
			if d := f.Verdict.Diagnosis; d != nil {
				fmt.Fprintf(out, " — %s %s", d.Reason, d.ModelVsReal)
			}
			fmt.Fprintln(out)
			if f.Verdict.PocDir != "" {
				fmt.Fprintf(out, "- PoC: `%s`\n", f.Verdict.PocDir)
			}
			fmt.Fprintln(out)
		}
	}
	section("Confirmed", s.Confirmed)
	section("Spurious", s.Spurious)
	section("Inconclusive", s.Inconclusive)
}
