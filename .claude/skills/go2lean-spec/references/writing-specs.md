# Writing go2lean specs

A spec is a Go function in a `_test.go` file of the target package. go2lean
compiles it into the model with the function it specifies, searches for
inputs that make it return false, and replays each one as a real Go test.
`go2lean fuzz` turns the same functions into `go test -fuzz` targets.

## Format

- **Marker.** `// go2lean:spec <Target>` in the doc comment, directly above
  the `func` line, with no blank line in between. A method is
  `T.M`.
- **Signature.**
  `func Spec<Target><What>(<the target's parameters, same names and types>, <its results>) bool`.
  - A single result is named `ret`; several are named `ret0`, `ret1`, …
  - For a method, the receiver comes first.
  - An error result is compared with nil only.
- **Return value.** Return true when the input is outside the property
  (its precondition does not hold). Return false only when the result is
  wrong.
- **Sequence specs.** `// go2lean:sequence <Target>`, free signature: take
  the events as a slice, drive the target once per event, and check an
  "always" property after each step and an "eventually" property at the
  end. The search draws up to 8 events and enumerates every sequence of up
  to 3 over the code's constants.
- **File contents.** Spec functions only. Never copy or redefine a
  function of the package. Import only what the parameter and result types
  need, and plain ASCII.

## What the model runs

Exact:
- integers of every width, bools, strings and their common operations;
- structs, pointers, arrays, slices, maps;
- interfaces, type switches, closures;
- `errors` compared with nil;
- `encoding/json`;
- the package and its imports, with `-deps`.

Not exact:
- **Floats are opaque.** Run float properties with `go2lean fuzz`.
- **Goroutines and channels** have their own checks (deadlock, leak, race);
  they are not spec material.
- **Time, I/O and the network** give unknown values.
- **Package-level variables** a spec reads are not modelled; use literals.
- **Ranging over a map** follows insertion order.

`docs/semantics.md` has the full table. When a spec needs something the
model lacks, write it anyway. The verdict comes back unknown or spurious,
and that is the signal to record the gap (interpreting-verdicts.md).

## Writing blind

Write from the docs, the signatures and the types. When the docs are
silent on a case, the spec says nothing about it (return true): an
undocumented edge is a question for the maintainers, not a bug.
Write down which doc sentence each spec encodes.

Read a function body only:
- to build an input value the API cannot otherwise make (a struct the
  constructor fills from the network);
- after a failure, to explain it.

## Making the search find things

- **State the general property, never a trigger value.** The search finds
  the inputs.
- **Make the variation an input.** When an input can take several legal
  shapes, derive the shape from a generated field (bits of an integer)
  instead of fixing one.
- **Normalise instead of skipping.** If a generated field would put the
  input outside the property, clear or fix it and check the input anyway.
- **Seed what the code does not contain.** The search draws constants from
  the code. If the bug class needs inputs the code never mentions
  (multibyte text, a huge length, `math.MaxInt`), put them in the spec as
  literals so the search can combine them.
- **Watch the vacuity count.** Each hunt's summary ends with
  `spec checked N, vacuous M` (trials that left through a `return true`
  guard). A spec that is almost all vacuous has not been checked.

## Spec review checklist

- Does it hold on the trivial inputs (zero values, empty strings and
  slices, nil)? Run it on them in a plain Go test before searching. In
  evaluation 5 most of the fuzzer's first failures were wrong specs at its
  first, zero-value input, and a search stops at its first failure.

- Does the spec recompute the function? Then it is a tautology: rewrite it
  as a relation or a comparison with a reference.
- Does it quote the doc line it encodes?
- Is it the strongest oracle the code supports (oracle-catalog.md)?
- Would a plausible bug in this function, such as an off-by-one, the wrong
  argument order, bytes for runes, or a dropped case, make it return
  false?
