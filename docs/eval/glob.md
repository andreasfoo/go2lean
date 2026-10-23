# Blind evaluation 2: gobwas/glob

The second run of go2lean on a codebase it was not built on, after the
first (docs/eval/semver.md) and the fixes that followed it. This page is
the protocol, committed before go2lean runs on the target; the results
section is added afterwards and the protocol is not edited after the run
starts.

## Target

[github.com/gobwas/glob](https://github.com/gobwas/glob) at
`7112f00494625902fcdb8fcdcd6638e0584a3acc` (2026-09-08): glob pattern
compilation and matching, about 2,000 lines in four packages (the root,
`syntax`, `internal/debug`, `cmd/globtest`). Chosen because it differs
from both earlier targets (a pattern compiler and matcher, several
packages), uses almost no `reflect` or `unsafe`, and has one Go fuzz
target, `FuzzMatchRegexp`, a differential property (a glob against the
regexp it translates to). Its matcher builds regexps at run time, which
go2lean's regexp model does not cover (constant patterns only): the run
shows what that limit costs.

Nobody involved has read its issue tracker or changelog before the run;
its source was read only to choose it (size, packages, fuzz target).

## Tool

go2lean frozen at the commit that adds this page (Go 1.24.7, Lean
v4.35.0), built once into a separate binary. Work on go2lean continues
during the run on later commits; the run uses only the frozen binary.
Model gaps, crashes and wrong verdicts are logged as tool issues and
fixed afterwards; a rerun after fixes is reported separately.

## Arms

A. **go2lean run.** `go2lean run -deps ./...` on the module with the
   default options: template properties, and the package's fuzz target
   read as a spec (`-fuzz-specs`, on by default).

B. **Agent.** `go2lean agent` on each package with exported functions
   (the root and `syntax`), dependencies modelled by default, with a
   Claude model as the spec writer through `-llm mock`: each prompt go2lean
   writes is answered by a fresh subagent that sees only that prompt, with
   no web access, no other files and no tools that run code. Proposal
   round, repair rounds as go2lean asks for them, report round. After the
   pipeline, go2lean hands every spec to `go test -fuzz` for 30 seconds
   (the agent's default `-fuzztime`).

C. **Native fuzzing baseline.** `go test -fuzz=FuzzMatchRegexp` for 10
   minutes on the same machine, not at the same time as A or B.

## Measures

Per arm: properties, counterexamples, verdicts (confirmed, spurious,
inconclusive), mutation scores of the specs that held, wall-clock time,
and for B the LLM calls and what the fuzzing step found. For B against C:
which violations each finds.

## Triage

As in docs/eval/semver.md: each confirmed result is a **bug** (contradicts
the package's documentation, the glob syntax it documents, or panics on an
input a caller can pass), **by design**, a **spec error**, or
**unreachable**, decided before the tracker is searched; then each bug is
**known** or **new** by the issue tracker (open and closed issues and pull
requests) and the history. Whether to report new bugs upstream is decided
after the run.

## Results

Run on 2026-10-23 with go2lean `3143cc8`, as the protocol above says, on
a 4-core machine. A and C ran alone; B's runs overlapped part of the time
with go2lean regression runs on the same machine, which lowers its
fuzzing step's execution counts (no other outcome depends on speed).

### Summary

| | A: `go2lean run` | B: agent (`syntax` + root) | C: Go fuzz, `FuzzMatchRegexp` |
|---|---|---|---|
| properties | 82 (81 templates + the imported fuzz target) | round 1: 28 specs (14 + 14), round 2: 33 (17 + 16), with the templates | 1 target |
| time | 37 s | 391 s + 290 s for both rounds and fuzzing, 4 LLM calls | 131 s (stopped at its first failure) |
| violations reported | 20 | round 1: 39, round 2: 46; fuzzing: 6 of 20 specs | 1 |
| confirmed by `go test` | 12 | round 1: 13, round 2: 15 | – |
| bugs in glob | 0 | 6 new, 1 known | 0 (1 in Go's `regexp`, known, fixed) |

The bugs, decided against the documentation before the tracker was
searched (open and closed issues and pull requests, 2026-10-23):

1. **Wrong match: `*{abc,b}*c??` does not match `abcXc`** (`regexp`
   `^.*(?:abc|b).*c..$` does). When the second star finds its literal,
   `storeStar` cuts the restart points back to the root's `starsFloor`
   and drops the first star's: resuming it with the shorter alternative
   would reach the second star earlier, which the reset assumes cannot
   happen. **New.** Found by the round-2 report, by reading the source;
   its spec for it (`SeqStarAltStar`) is right (it fails on its own
   example) but no search found an input: none in go2lean's 1,848 random
   trials, none in 60 s of Go fuzzing.
2. **A class with an escaped `-` does not compile**: `[\-]` is a syntax
   error ("unexpected end of input"), though the grammar says `\` c
   matches c inside a class and `-` is literal in a set. `fetchRange`
   peeks for `-` after reading the `\` and takes the `\` as a range's low
   end. **New.** Found by Go's fuzzer on `SeqMatchAgreesWithReference`
   (round 1); go2lean's search found it too after the fixes below.
3. **A `}` outside a group compiles as a literal**, though the grammar
   excludes `}` from literal characters (`unexpected }`, `a}b`); the
   parser's own "unexpected `}`" error never fires, because the lexer
   emits the byte as text. **New**, minor. Found by go2lean's search on
   `SpecCompileBraceBalance` (round 1), confirmed.
4. **A separator that is not a valid rune**: `*` treats it as U+FFFD
   (`sepStr = string(sep)`, searched with `strings.IndexAny`, which reads
   an invalid byte as U+FFFD), `?` compares runes and does not; with
   separator `0x303030`, `*` rejects `"\xe7"` while `*?` and `?` accept
   it. **New**, minor (`Compile` accepts the separator silently). Found by
   Go's fuzzer on `SeqStarSeparators` (round 1).
5. **The lexer replaces an invalid UTF-8 error with a later one**:
   `"\\\xff"` reports "trailing backslash", `"[\xff"` "unexpected end of
   input" (`errorf` overwrites `l.err`). **New**, minor. Found by the
   round-1 report reading the source; its round-2 spec
   (`SeqInvalidUTF8IsReported`) was confirmed by go2lean's search.
6. **`Lexer.Offset` after a `[` is the end of the class**, not "right
   after the most recently returned token" as documented (the whole class
   is lexed at once); error offsets point past the class. **New**, minor.
   Found by Go's fuzzer (round 1, on `[]`, three specs);
   `SeqOffsetRightAfterRangeOpen` (round 2) confirmed by go2lean.
7. **`QuoteMeta` does not escape `,`**, so a quoted string with a comma
   splits into alternatives inside `{...}`. **Known**: issue #78 (open,
   reported on the same commit by another property-based testing tool),
   pull requests #79, #80. Triaged before the search as by design
   (`IsSpecial`'s doc says `,` is not special); the tracker counts it as a
   bug. Found by Go's fuzzer on `SeqAlternativesExact` (round 1) and by
   go2lean's search after the fixes (`SeqQuotedGroupMatchesItself`).

Not counted: the lexer does not report the empty class `[]` (the parser
does: "could not parse range"), while `TokenType`'s doc says the lexer
catches malformed classes: a doc inaccuracy. And the round-2 report's
second source-only claim, that `commonSuffix` treats any two invalid
bytes as equal, is unreachable: a pattern with invalid UTF-8 does not
compile.

Not in glob: **Go's `regexp` matches `(?:[aA]0|A)` against `a`**: when
factoring a common prefix out of an alternation, `regexp/syntax` takes
`A` and the case-folded `[aA]` as the same literal. **Known**:
golang/go#59007, fixed after Go 1.25 (Go 1.24.7 and 1.25.3 match, 1.26.0
does not). Found by C: glob's fuzz target compares `Match` with the
`regexp` it translates the pattern to, and on `{{a,A}0,A}` the regexp is
wrong, not glob.

### A: `go2lean run`

82 properties in 37 s. 20 counterexamples: 12 confirmed, 8 spurious.

The 12 confirmed are no bugs, all unreachable: internal functions given
states `Compile` never builds (`matchContext.branch`, `storeStar`,
`frame.index`, `matchState.allocPath`, `starMatcher.Match` and
`storeSkip` with negative depths or offsets; `altMatcher` and
`multiMatcher` with nil children; `tokens.shift` on an empty queue, which
`Lexer.Next` checks first; `cmd/globtest`'s `pointAt` with a negative
error offset). The native fuzzing of C, ten million executions through
`Compile` and `Match`, panicked nowhere.

The 8 spurious: 7 from tool issue 1 (the lexer's breaker tables and
`unicode/utf8`'s lookup tables unknown: every lexing property), 1 the
imported fuzz target (run-time regexps, outside the model, as the
protocol expected). Tool issue 2 kept `Pattern.Match`, the package's
main function, out of the model entirely.

### B: agent

`syntax`: 14 specs (round 1), 17 after the report round. Round 1: 1
confirmed (`tokens.shift`, as in A), 17 spurious (tool issues 1 and 3);
Go's fuzzer, given the 14 specs, violated 3 within seconds, all on `[]`
(bug 6). The report round split those specs per behaviour, found bug 5
in the source and added specs for it; round 2 confirmed bug 5, bug 6 and
the empty-class doc inaccuracy.

Root package: 14 specs (round 1), 16 after the report round. Round 1:
12 confirmed (the 10 unreachable internals of A, bug 3, and bug 7 through
`SeqAlternativesExact`), 9 spurious (tool issues 1 to 4); Go's fuzzer
violated 3 of the 6 specs it can take: bugs 2, 4 and 7. Round 2: 11
confirmed (the 10, bug 3), 14 spurious. The report traced bug 1 in the
source and wrote `SeqStarAltStar` for it; it also correctly sorted the
round-1 results (spec glitch vs package bug in
`SeqMatchAgreesWithReference`, unreachable internals) and made one wrong
claim (`commonSuffix`, above).

Mutation scores were rare in the frozen run because few specs held: 24
of 24 (`TokenType.String`), 8 of 12 (`commonSuffix`), 1 of 1
(`SyntaxError.Error`).

### C: native fuzzing

9.7 M executions in 131 s, then the failure above (Go's `regexp`). Go's
fuzzer stops at its first failure; the protocol's 10 minutes were not
completed and the run was not restarted (its corpus would replay the
failure first).

### Reading

- **The agent found the bugs again; templates found none.** Same as on
  semver. Every glob bug came from the agent's specs (5) or from its
  reading of the source (2: bugs 1 and 5).
- **The most serious bug was found by reading, not by search.** Bug 1
  (a wrong match on a valid pattern) was reasoned out of the source in a
  report round, and the spec written for it is right, but neither
  go2lean's search nor Go's fuzzer reached the input. The agent wrote the
  failing input in the spec's comment: a spec writer should be able to
  give the search its seeds (tool issue 6).
- **Go's fuzzer on the agent's specs was quicker than go2lean's search in
  the frozen run** (bugs 2, 4, 6, 7 in seconds), because go2lean's model
  had three gaps that made most lexing and matching runs spurious. After
  the fixes, go2lean's search finds 2, 3, 5, 6 and 7 itself and the
  fuzzer 2, 4 and 7: they overlap and each finds one the other does not.
- **The package's own fuzz target found a bug in the standard library**
  (known and fixed upstream), not in glob: a differential oracle is only
  as right as its reference.
- **Model gaps decided the frozen run again**, but generic ones: tables
  built by variable initializers, `defer` of a modelled function, and a
  model bug in unsigned indexing that the first fix uncovered.

The agent's spec files (round 2) and reports are in [`glob/`](glob/).

### Tool issues

1. Package-level data tables built by variable initializers were
   unknown: slice literals and appends of them (the lexer's breakers,
   `specials`), arrays of struct literals (`unicode/utf8`'s
   `acceptRanges`). Every lexing run read zeros or got stuck in
   `utf8.DecodeRuneInString`.
2. `defer` of a modelled function rejected the whole function:
   `Pattern.Match` (`defer releaseState(state)`) was never modelled, so
   every spec that matches got stuck.
3. A model bug the fix of issue 1 uncovered: an index of an unsigned type
   narrower than 64 bits (a `byte` indexing `utf8`'s 256-entry table) was
   sign-extended, so a lead byte from 0x80 up indexed out of range.
4. `internal/bytealg.IndexByte` (under `strings.IndexByte`,
   `bytes.IndexByte`) was unmodelled. Already being fixed by the
   standard-library audit when the run started, not prompted by it.
5. `fmt.Sprintf` with `%v` of a type with a `String` method, and `%q`,
   are not modelled (`Token.String`): 1 spurious per run.
6. A spec writer cannot hand the search seed inputs; `SeqStarAltStar`'s
   comment names the failing input that no search found.
7. A sequence spec that calls through a function value the model does
   not know (`SeqEndIsSticky`) stays spurious.

## Rerun after the fixes (not part of the frozen run)

Issues 1 to 4 fixed afterwards (go2lean, the commit that adds this
section), each generic and covered by an example package and the
regression gate; then the agent's round-2 spec files of both packages
run again with `go2lean run -deps github.com/gobwas/glob/...` (no LLM
calls) and `go2lean fuzz` (60 s per spec). Not comparable to the frozen
run as a measurement: the tool was changed after looking at this target.

142 s. 23 counterexamples: 20 confirmed, 3 spurious (issues 5 and 7, and
the imported fuzz target's run-time regexps). The 20 confirmed: the 12
unreachable internals of A, `Pattern.Match` on a `Pattern` built field by
field with a nil matcher (unreachable), `MustCompile` panicking on a bad
pattern (by design), and 6 spec violations: bugs 2, 3, 5, 6 and 7 and
the empty-class doc inaccuracy. Bug 1's spec held (none in 1,848 random
trials) and bug 4's (none in 2,000). Go's fuzzer on the root specs:
bugs 2, 4 and 7 again; `SeqStarAltStar` held 60 s.

Mutation scores of the specs that held, out of 24 mutants unless stated:
`TokenType.String` 24; lexer sequence specs 13, 10, 10, 9, 8, 7, 7, 3,
2; `SeqAlternativesExact` 7, `SeqStarAltStar` 5, `SeqStarSeparators` 4;
`Compile`'s literal, result-shape and `MustCompile` agreement specs 1
each (they check almost nothing a one-line bug in `compile` would
break); `QuoteMeta` round trip and unescape 7 of 8; `commonSuffix` 8 of
12; `IsSpecial` and `SyntaxError.Error` 1 of 1.
