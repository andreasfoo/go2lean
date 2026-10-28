# Evaluation 5: go2lean's search against go test -fuzz, same specs

Issue #37 asks what go2lean adds over simpler ways of finding defects. A
spec helps every method that can run it, so this run holds the spec fixed
and compares only what searches it: go2lean's model search (code
constants, small exhaustive domains, then replay in real Go) against
Go's native fuzzer. This page is the protocol, committed before any spec
is written; results are added afterwards and the protocol is not edited
once the run starts.

## Cases

Real wrong-result defects, from fix commits of open-source Go libraries:
- **Pool.** Commits whose subject reads like a fix, that change 1 to 3
  non-test Go files and add or change a test
  (`search-vs-fuzz/mine.py`, drawn by `search-vs-fuzz/draw.py` into
  `search-vs-fuzz/pool.jsonl`), from 54
  libraries cloned for earlier evaluations, at most 3 per library, in a
  seeded random order (seed 20261028). Network clients and servers
  (go-redis, fasthttp, the Prometheus client) are left out: their fixes are
  rarely reachable through generated values.
- **Screening.** Read in that order, until 30 are accepted. A case is
  accepted when:
  - the fix changes a result, not only a panic, a race, a leak or speed;
  - one exported function or method shows it on values a generator can
    build;
  - the parent commit's documentation, or a well-known reference, says
    what the right result is;
  - the fix's test fails on the parent and passes on the fix.

  An agent screens and writes two files: the public case list (repository,
  parent commit, package, target function, where its documentation or
  reference is) and the private ground truth (the fix, the bug, the
  triggering input). The spec writer reads only the first until the run
  ends. Every rejection is recorded with its reason.

## Specs

For each case, one spec of the target, written by one writer (Claude, in
the session that runs the evaluation):
- from the parent commit's documentation, signatures and types, or the
  reference, with the skill `.claude/skills/go2lean-spec`;
- without reading the target's body, the fix, its test, the commit log
  after the parent, or the issue tracker;
- as a `go2lean:spec` function in a `_test.go` file of the target's
  package, the documentation line it encodes quoted in its comment.

The writer knows the target has a defect; that is the same for both
arms. A spec that does not compile, or that cannot express the oracle, is
recorded as such and the case counts as not found by both arms.

## Arms

Both arms get the same spec file, on the parent commit, on the same
machine, one after the other.

- **A, go2lean.** `go2lean run -deps -budget 300 -prove=false`, the
  target's package only. Found: a confirmed counterexample of the spec.
- **B, fuzz.** `go2lean fuzz -fuzztime 5m` on the same spec. Found: a
  failing input of the spec.

Seeds: B starts from the package's existing fuzz corpus
(`testdata/fuzz`), if any; `go2lean fuzz` adds none. A draws from the
code's constants and the spec's literals. That difference is part of what
is measured: it is how each tool ships.

## Scoring

A failing input counts as the defect when:
- the spec holds for that input on the fix commit;
- or, where the spec is wrong for that input on both commits, it does not
  count, and the case is marked "spec wrong".

Per case and arm, the score is found, not found, or tool failure, with the
time to the first failure.

Reported:
- found by A only, by B only, by both, by neither;
- for "neither", whether the spec could catch the defect at all: does it
  fail on the triggering input from the ground truth?
- every tool failure of A (model gaps, from `scripts/gaps.go`), since
  those are what stage 1 is extended from;
- float cases separately, as the model treats floats as opaque.

The hypothesis to test: A finds defects that need a specific combination
of the code's constants (a boundary, a delimiter, an exact length) where
B's random mutation rarely lands, and B finds what A's model cannot run.
If A finds nothing B misses, that is the result.

## Tool

go2lean frozen at the commit that adds this page, built once into a
separate binary. Go and Lean as in the earlier evaluations.

## Cost

No LLM is called by either arm. The screening agent is one run, about
0.5 to 0.8 M tokens; the specs are written in the evaluating session.
