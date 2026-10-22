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

(Added after the run.)
