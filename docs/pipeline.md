# Pipeline and specifications

[← Back to README](../README.md)

[How it works](#how-it-works) · [Writing a spec](#writing-a-spec)

## How it works

![The go2lean pipeline: human- or agent-written Go specs compile with the source; built-in specs also guide Lean search; candidate inputs are replayed with go test.](../docs/images/how-it-works.svg)

**Specs enter in two ways:** built-in properties such as `no_panic` are
generated for eligible targets; business specs are Go functions written by
a human or the optional agent. Go specs pass through the compile stage alongside
the target source, then both kinds of properties guide the spec & search stage.
A model counterexample is only a candidate until the replay stage checks it
with the real Go toolchain.

### Compile stage

`golang.org/x/tools/go/ssa` lowers the package; the
frontend turns it into a frontend-independent IR (blocks with parameters,
one dialect op per instruction) and prints it as a Lean term over the `GoSSA`
dialect. The Lean side is a small SSA core in the spirit of
[lean-mlir](https://github.com/opencompl/lean-mlir): a dialect-parametric
program representation with a fuel-bounded, total interpreter. Slice contents
come from a read-only memory oracle fixed per run; values the frontend cannot
model are explicit havoc inputs. The model may over-approximate; it never
silently guesses. Lean core only — no Mathlib.

### Spec & search stage

Two property shapes are templated:
`no_panic` for every modelled function, and `postcondition` for every
**spec-in-Go** function — a Go function with a `// go2lean:spec Target` doc
comment and signature `func Spec(<Target params>..., ret <result>) bool`,
compiled into the model with the same pipeline as the target so the Lean
property and the Go oracle agree by construction. Two engines run per
property: seeded random search (per-trial value profiles, plus the target's
own integer literals so magic-constant branches are reachable), then
exhaustive enumeration of a small domain (narrow signatures only), which
records an `exhausted` certificate when it passes (`exhausted_havoc` when
some inputs ran through values the model does not know, of which it tried
only a few; `exhausted_assuming`, with the list, when some runs took an
under-approximation such as a dropped store to a global). When it passes on
a `no_panic` property of a function of integer and bool parameters, a Lean
theorem over every input is generated and checked, and the result becomes
`proved`, with the trust base `kernel` or `compiler` read from
`#print axioms` (design doc §5.3). For a loop-free function (integers,
bools, strings and integer slices), `no_panic` or a spec is stated as the
function's contract for every input and every value the model does not
know, proved by `wp_auto`, and recorded `proved` only when Lean's
comparator accepts it, with a certificate ([trust](trust.md)). A third property shape,
`go2lean:sequence`, states what must hold over a sequence of calls (an
invariant after every event, a condition at the end of the stream) and is
checked by bounded model checking over short sequences (design doc §5.2). A spec whose verdict
depended on a havoc value (it looked inside an opaque value, or called
something unmodelled) is reported as unknown, never as a counterexample
or a certificate. Both run in `go2lean-search`, a native
executable built by lake from the Lean engines and every generated model,
driven by a JSON description of the search; properties are searched in
parallel, and each engine stops at a wall-clock budget (10 s by default; 120 s
under `agent`)
so one function with expensive inputs cannot stall a sweep. go2lean keeps its own memory under a
third of physical memory (a soft limit; `GOMEMLIMIT` overrides it), and
`GO2LEAN_MEMPROFILE=<file>` writes a heap profile near a run's peak. The agent sits on top: it reads the source and
the model report, writes specs into `<pkg>/go2lean_specs_test.go`, repairs
the ones the frontend rejects, runs the pipeline, and writes a report from
the verdicts.

### Replay stage

A counterexample becomes a Go test in the target package,
which executes the real function with the generated inputs: run go2lean on
a scratch clone, since a function that writes files will write them. The
test never fails by itself; it prints what happened and the `poc` package
judges: `confirmed` (the real run violates the oracle the way the property
says), `spurious` (it does not — with a diagnosis of what the model got
wrong), or `inconclusive`. This is the only source of trust: a bug is
reported only with a verdict from here.

For a deadlock or goroutine leak the call runs up to 200 times under a
watchdog; when Go's scheduler never takes the model's interleaving, the
replay stage replays the model's schedule: the package is rewritten
through an overlay so that each channel, lock and `select` operation waits
for its turn, and the real runtime then shows the deadlock or leak. The
PoC carries the overlay ([concurrency.md](concurrency.md)).

## Writing a spec

You do not need to write Lean. There are two different ways to state a claim:

| Claim | How it is represented | What violates it |
|---|---|---|
| The function must not panic | Auto-generated `no_panic` property; no spec function required | A panic during the target call, confirmed by real Go replay |
| The result must satisfy a business rule | A Go function marked `// go2lean:spec Target` | The spec returns `false`, confirmed by real Go replay |

For example, the included [`Sum` spec](../examples/overflow/overflow.go) says
that adding non-negative integers should produce a non-negative result:

```go
// go2lean:spec Sum
func SpecSum(xs []int, ret int) bool {
    for _, x := range xs {
        if x < 0 {
            return true // This rule does not constrain inputs with negative elements.
        }
    }
    return ret >= 0
}
```

The annotation associates the spec with `Sum`. Its arguments are the target's
inputs followed by its result; `true` means the rule holds for that execution.
Place the spec in the target package; the agent writes its specs to
`go2lean_specs_test.go`. The compile stage translates the spec alongside the
target; the spec & search stage looks for inputs that make it return `false`;
the replay stage checks the same oracle in Go.

**Why not just use `no_panic`?** Go integer overflow wraps without panicking.
The business spec detects a wrong result that a panic check cannot catch.
Conversely, a target panic is checked by `no_panic`, not by pretending the
postcondition returned `false`.

**A claim is not a proof.** Finding no counterexample in a bounded search
is not a guarantee for all inputs. See [limitations](../docs/limitations.md)
for model boundaries and the restricted cases supporting all-input proofs.
