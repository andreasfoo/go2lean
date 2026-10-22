# Blind evaluation 1: Masterminds/semver

The first run of go2lean on a codebase it was not built on
(docs/limitations.md §4). This page is the protocol, written and
committed before go2lean was run on the target; the results section is
added afterwards and the protocol is not edited after the run starts.

## Target

[github.com/Masterminds/semver/v3](https://github.com/Masterminds/semver)
at `dd2b995c61c39ddd668b23ac919b04d607be35ab` (2026-08-14): semantic
version parsing, comparison and constraint checking, about 1,700 lines in
one package, used by Helm and many Go tools. Chosen because it differs from
tingly-box in kind (a parser library, not a gateway), its code is plain
functions over strings and integers (go2lean's strong case: almost no
`reflect` or `unsafe`), and it has three Go fuzz targets
(`FuzzNewVersion`, `FuzzStrictNewVersion`, `FuzzNewConstraint`) to compare
against. It is mature and fuzzed already, so a low yield is a possible and
informative result.

Nobody involved has read its issue tracker or changelog before the run.
Its source was read only to choose it (size, imports, fuzz targets).

## Tool

go2lean frozen at the commit that adds this page (Go 1.24.7, Lean
v4.35.0). No change to go2lean between the start of the run and the
recording of the results. Model gaps, crashes and wrong verdicts found
during the run are logged in the results as tool issues; they are fixed
afterwards, and a re-run after fixes is reported separately, never merged
with the frozen run's numbers.

## Arms

A. **Templates.** `go2lean run -deps` on the package with the default
   search options (2,000 random trials, exhaustive up to 300,000 inputs,
   10 s per engine and property). No specs: the properties go2lean
   proposes by itself (no panic, and the templates it applies).

B. **Agent.** `go2lean agent` on the package (dependencies modelled by
   default), with a Claude model as the spec writer through `-llm mock`:
   each prompt go2lean writes is answered by a fresh subagent that sees
   only that prompt (which includes the package source), with no web
   access and no other files. The proposal round and the report round as
   go2lean runs them; repair rounds as go2lean asks for them. One run.

C. **Native fuzzing baseline.** `go test -fuzz` on the same machine:
   1. each of the three existing fuzz targets for 10 minutes;
   2. each spec the agent wrote in arm B whose parameters Go's fuzzer
      accepts (strings, byte slices, integers, floats, bools), wrapped
      mechanically as `f.Fuzz(func(t *testing.T, args...) { if
      !Spec(args...) { t.Fatal() } })`, for 5 minutes. A spec Go's fuzzer
      cannot take (a slice of structs) is reported as such.

## Measures

Per arm: properties, counterexamples, verdicts (confirmed, spurious,
inconclusive), wall-clock time, and for B the LLM calls. For A against
C.1 and B against C.2: which violations each side finds, and the time to
the first one.

## Triage

Each confirmed result gets one label, by these rules, before the tracker
is searched:

- **bug**: behaviour that contradicts the package's documentation
  (README, doc comments) or the Semantic Versioning 2.0.0 specification
  where the package says it follows it, or a panic an exported function
  raises on an input a caller can pass (a user-supplied version or
  constraint string);
- **by design**: documented behaviour (a documented panic, a documented
  leniency such as the coercion `NewVersion` does);
- **spec error**: the spec states something the documentation does not
  promise;
- **unreachable**: an input no caller of the exported API can build.

Then the issue tracker (open and closed issues and pull requests) and the
changelog are searched for each bug: **known** or **new**. New bugs are
written up with a failing `go test`; whether to report them upstream is
decided after the run, not by this protocol.

## Results

Run on 2026-10-22 with go2lean `9dcbc8b`, as the protocol above says,
on a 4-core machine. Arms ran one after the other (no two searches at
once), except that go2lean wrote the agent's first prompt (one compile,
no search) while C.1 was fuzzing.

### Summary

| | A: templates | B: agent specs | C.1: Go fuzz, existing targets | C.2: Go fuzz, agent specs |
|---|---|---|---|---|
| properties / targets | 61 | 21 (+ the 61 of A) | 3 | 3 (+ 7 adapted, extra) |
| time | 53 s | 129 s, 2 LLM calls | 3 × 10 min | 3 × 5 min (+ 7 × ≤ 5 min) |
| violations reported | 42 | 11 | 0 (85 M executions) | 0 (+ 4 of 7 adapted) |
| confirmed by `go test` | 8 | 1 | – | – |
| bugs | 0 | 1 (half new) | 0 | 0 (+ the same 1, and 1 new minor) |

The bugs, all in constraint checking with a bare wildcard (`*`, `x`,
`X`), which README and doc.go say "works for all comparison operators",
with `*` meaning `>= 0.0.0`:

1. **`^*` / `^x` match only `0.0.0`** (the code's own comment says
   `^* --> (any)`). **Known**: issue #317 (opened 2026-09-19, after the
   target commit), pull requests #316, #319, #321.
2. **`<=*` / `<=x` / `=<*` reject every version from `0.1.0` up** (the
   wildcard becomes `0.0.0` and `<=` compares the major and minor).
   **New**: no issue or pull request found; the #317 fixes touch
   `constraintCaret` only.
3. **`!=*` / `!=X` behave like `!= 0.0.0`**: they accept `1.2.3`, which
   `=*` also accepts. **New**, minor (a constraint nobody needs). Found
   only by C.2's extra run.

Found by arm B: `SeqWildcardMatchesEveryRelease`, which checks bugs 1
and 2 together; the counterexample's operator is not named by the run,
and a Go probe shows both fail. The report round judged it a real bug.

Also real, found by no arm: numeric prerelease identifiers too large for
`uint64` are compared as text (`1.0.0-100000000000000000000` <
`1.0.0-99999999999999999999`). The agent predicted it from the source;
its spec (`SpecVersionComparePrecedence`) ran vacuously in go2lean (78
of 2,000 runs checked) and takes `*Version`, which Go's fuzzer cannot.
**Known**: pull requests #315, #323.

### A: templates

96 functions modelled (78 complete, 12 partial, 6 rejected), 61
properties, 7 certificates (1 `proved`: `compareSegment` over every
input; 1 `exhausted`, 2 `exhausted_assuming`, 3 `exhausted_havoc`).

42 counterexamples: 8 confirmed, 34 spurious. The 8 confirmed are no
bugs: `Collection.Less` / `Swap` with negative indices (by design: the
`sort.Interface` contract); `Constraints.Check` / `Validate` on a
receiver built field by field with `containsPre` out of step
(unreachable: only `NewConstraint` builds it); `Constraints.String` /
`MarshalText` with a nil `*constraint` inside (unreachable);
`MustParse` (by design: documented panic); `validateVersion([]string{})`
(unreachable: its one caller passes a regexp submatch of fixed length).
The ranking put four of them in tier 1.

The 34 spurious all come from one tool issue (1 below): every function
that parses goes through a regexp the package assigns in `init()`.

### B: agent

21 specs from one blind proposal round: 14 postconditions and 7
sequence specs (constraint checks take a constraint string and versions,
so the agent wrote them as sequences over `(major, minor, patch)`
triples). Outcomes: 1 confirmed (the wildcard bug), 10 spurious, 10
without counterexample (of which `SpecStrictNewVersionAgreesWithNewVersion`
and the three marshalling round trips checked nothing: every run was
vacuous or stuck). The 10 spurious are tool issue 1 again: the model
does not know the regexps, so `NewConstraint` and `NewVersion` do not
compute what Go computes.

The report round (a second blind subagent) judged the confirmed result a
real bug, the eight confirmed template results spec or input errors, and
the spurious ones tool errors; it did not revise the specs, because the
report prompt does not contain the spec file (tool issue 4).

### C: native fuzzing

C.1: `FuzzNewVersion` 30.4 M executions, `FuzzStrictNewVersion` 35.1 M,
`FuzzNewConstraint` 19.7 M, 10 minutes each: no failure. The targets only
check that parsing does not panic; go2lean's three matching `no_panic`
properties found nothing either (`StrictNewVersion`: none in 2,000
trials, exhaustive on 171 inputs) or stopped at a spurious result.

C.2 under the protocol: of the 21 agent specs, 3 take parameters Go's
fuzzer accepts (the postconditions on `New`, `StrictNewVersion`,
`MustParse`); 5 minutes each (13.8 M, 16.4 M, 10.6 M executions), no
failure. The other 18 take a `*Version`, a `Version`, a `Collection`, or
(the 7 sequence specs) a `[]uint64`.

Extra, beyond the protocol: the 7 sequence specs with their `[]uint64`
decoded from a `[]byte`. Go's fuzzer reported 4 of them violated within
62 s (most in under a second): the wildcard spec (bugs 1 and 2), the two
complement specs (bug 3), and `SeqTildeMatchesDocumentedRange` (`~0.0.0`
accepts every version: by design, a special case the code documents).
The other 3 held for 5 minutes.

### Reading

- **The agent's specs carried the result, not go2lean's search.** Once
  the specs existed, Go's own fuzzer found everything go2lean confirmed
  and one more bug, in seconds. go2lean's model, blocked by tool issue 1,
  confirmed 1 of the 4 violated specs. On this package the value was in
  writing the specs, and go2lean's prompt and agent loop did that well:
  blind, two LLM calls, 21 specs, three real behaviours found.
- **One model gap decided the run.** Regexps compiled in `init()` are a
  common Go idiom; the model does not run `init()`, so 44 of the 53
  counterexamples were spurious. That is the first fix.
- **Templates alone found nothing**, here as on tingly-box: the confirmed
  `no_panic` results are contract violations by the test, and the ranking
  did not see that.
- **Go fuzzing and go2lean should be one tool, not rivals**: go2lean
  writes and checks the specs, and handing every spec to `go test -fuzz`
  as well costs little and would have found bug 3.

The agent's specs, its report and the fuzz wrappers are in
[`semver/`](semver/).

### Tool issues (not fixed during the run)

1. Package `init()` functions are not run: variables they assign (here
   `versionRegex`, `constraintRegex`, ...) are unknown to the model. 44
   spurious counterexamples.
2. The spurious-verdict note (since `87a0963`) said "compiling with -deps
   models the functions": wrong here, the run already had `-deps`; the
   unmodelled values were the regexps from `init()`.
3. Ranking: `sort.Interface` methods called with out-of-range indices,
   and methods on an exported type whose constructor's name does not
   match it (`NewConstraint` for `Constraints`), are ranked tier 1 though
   no caller can pass those inputs.
4. The report round's prompt does not include the spec file, so the
   agent cannot revise specs without rewriting all of them.
5. A random search stops at its first counterexample, so one spurious
   result hides a property's real ones (every `NewVersion` wrapper
   stopped at trial 0).
6. Sequence specs over `[]uint64` and specs over `*Version` cannot be
   handed to Go's fuzzer as they are; an export to fuzz targets would
   need a byte decoding like the one used above.
