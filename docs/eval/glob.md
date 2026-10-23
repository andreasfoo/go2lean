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

(Added after the run.)
