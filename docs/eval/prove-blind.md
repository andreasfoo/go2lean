# Blind evaluation 3: `go2lean prove` on real code

The first run of `go2lean prove` on code go2lean was not built on: so far
it has proved only functions written for it (`examples/*`). This page is
the protocol. It is committed before `prove` runs on any target; the
results section is added afterwards, and the protocol is not edited after
the run starts.

## Targets

Three small, widely used Go modules of plain functions over strings and
integers:

| module | commit | lines (non-test) | what it is |
|---|---|---|---|
| [lithammer/fuzzysearch](https://github.com/lithammer/fuzzysearch) | `36fdf3c` | 337 | fuzzy string matching and ranking (`fuzzy`) |
| [agnivade/levenshtein](https://github.com/agnivade/levenshtein) | `c4a79bf` | 118 | edit distance |
| [xrash/smetrics](https://github.com/xrash/smetrics) | `39c47d9` | 546 | string metrics: Soundex, Wagner–Fischer, Ukkonen, ... |

**How the targets were chosen.**
- Of nine modules surveyed, these have the most exported functions inside
  what `prove` states: non-methods whose parameters are integers, bools,
  strings or integer slices, with at most one result of those kinds.
- The survey was a mechanical filter on signatures (`go/types`).
- Nobody involved has read their function bodies, issue trackers or
  changelogs.

**The functions in scope.** All 13 that pass the filter:
- `fuzzy`: `Match`, `MatchFold`, `MatchNormalized`,
  `MatchNormalizedFold`, `RankMatch`, `RankMatchFold`,
  `RankMatchNormalized`, `RankMatchNormalizedFold`,
  `LevenshteinDistance`.
- `levenshtein`: `ComputeDistance`.
- `smetrics`: `Soundex`, `Ukkonen`, `WagnerFischer`.

A Lean postcondition about a string result has to go through the heap; a
Go spec takes it as a string.

## Tool

**The build.** go2lean is frozen at the commit that adds this page (Go
1.26, Lean v4.35.0), built once into a separate binary.

**The agent.** The proof agent is the claude CLI's default model
(`-llm claude-code`), with:
- Lean tools (`-lean-tools`);
- `-rounds 6`;
- `-budget 10m`;
- the judge at its default (`lake comparator --paranoid`).

**Tool issues.** Model gaps, crashes and wrong verdicts are logged as tool
issues and fixed afterwards. A rerun after fixes is reported separately,
never merged with the frozen run's numbers.

## Arms

**A. No panic.** For each function in scope, a goal with postcondition
`True`. The theorem then says: for every input, every value the model does
not know and every run that ends, no panic and no under-approximation.
- Loop-free functions are proved by `wp_auto` alone.
- A function with loops goes to the proof agent.

**B. Documented contracts.**

1. **Specs from the docs alone.** A fresh subagent sees only `go doc -all`
   of each package: doc comments and signatures, no function bodies. It
   writes Go specs (`go2lean:spec`) for what the documentation promises.
   It has no web access, no files and no tools that run code.
2. **The search first.** `go2lean run -deps` searches every spec on a
   scratch clone. A confirmed counterexample is triaged as in
   `docs/eval/semver.md`: a bug, or a spec that misreads the docs. Either
   way it is not sent to `prove`.
3. **Then the proof.** Every spec with no counterexample goes to
   `go2lean prove`, with the same settings as arm A.

## Measures

**Per goal:**
- the outcome:
  - proved, with the judge's certificate;
  - or open, with the reason as one of:
    - (a) the function, or one it calls, is not modelled completely;
    - (b) the statement is not supported (a parameter or result kind);
    - (c) the property is false (a confirmed counterexample);
    - (d) the agent found no proof within its rounds;
    - (e) a tool error;
- the agent's rounds, time and tool turns.

**Per proved goal:** the property run as a Go fuzz target for 60 seconds
on the real code. A counterexample would be a soundness failure, and is
reported first.

**Overall:** the fraction proved per arm, and the reasons the rest stay
open.

## Expectations, written before the run

- `fuzzy`'s `Normalized` variants go through `golang.org/x/text`'s
  `transform` and `unicode/norm`, which go2lean does not model, so they
  stay open with reason (a).
- The edit distances are nested loops over `[]rune` with a row slice. They
  are the hardest proofs here and the most informative.
- `Soundex` returns a string and works over bytes. Its `no_panic` is in
  reach.

## Results

Added after the run.
