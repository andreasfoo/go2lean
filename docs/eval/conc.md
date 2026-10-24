# Blind evaluation 3: concurrent code

The third run of go2lean on code it was not built on, and the first on
concurrent code: the concurrency support (C1: deadlocks, goroutine leaks
and misuse panics; C2: data races; both confirmed by `go test`, with the
model's schedule replayed when Go's scheduler does not take it) was built
against GoKer's kernels and `examples/conc`. This page is the protocol,
committed before go2lean runs on the targets; the results section is
added afterwards and the protocol is not edited after the run starts.

## Targets

- [github.com/eapache/go-resiliency](https://github.com/eapache/go-resiliency)
  at `39c0aa4a7ec871cc8e2f7be2c44db1362e0667b7` (2025-02-23): resiliency
  patterns, 645 lines in five packages (`breaker`, `semaphore`,
  `batcher`, `deadline`, `retrier`) built on goroutines, channels,
  mutexes, `sync/atomic` and timers.
- [github.com/patrickmn/go-cache](https://github.com/patrickmn/go-cache)
  at `46f407853014144407b6c2ec7ccc76bf67958d93` (2019-10-04): an
  in-memory cache, 1,353 lines in one package, a map behind a
  `sync.RWMutex` with a janitor goroutine on a ticker and a sharded
  variant. The repository predates modules: the run adds
  `go.mod` (`module github.com/patrickmn/go-cache`, `go 1.13`) and
  changes no source.

Chosen because both are small, widely used, concurrent through and
through, and use no generics, `reflect`, `unsafe` or networking, so a
miss is the concurrency model's, not an unrelated gap's. Nobody involved
has read either issue tracker or changelog before the run; the sources
were read only to choose them (size, imports). Both are well known, so
some of their history may be in the model's training data; the triage
below records findings before the tracker is searched.

## Tool

go2lean frozen at the commit that adds this page (Go 1.24.7, Lean
v4.35.0), built once into a separate binary. Work on go2lean continues
during the run on later commits; the run uses only the frozen binary.
Model gaps, crashes and wrong verdicts are logged as tool issues and
fixed afterwards; a rerun after fixes is reported separately.

## Arms

A. **Exported API.** `go2lean run -deps ./...` on each module with the
   default options: template properties (`no_panic`, which covers
   deadlocks, leaks and data races in functions that synchronise) on the
   exported functions and methods.

A'. **The packages' own tests.** As A, plus `-tests`: each `TestXxx`
   becomes a target, as GoKer's kernels are run.

C. **Baseline.** What a developer already has: `go test -race -count=20
   ./...` on each module, on the same machine, not at the same time as A
   or A'.

The agent arm of the earlier evaluations (an LLM writing specs) is left
out: its specs are functional, and this evaluation is about the
concurrency templates.

## Measures

Per arm and module: targets, functions modelled (complete, partial,
rejected), properties, counterexamples by kind, verdicts (confirmed,
spurious, inconclusive) and whether a confirmation needed the replayed
schedule, wall-clock time. For C: failures and race reports. A against
C: which bugs each finds.

## Triage

As in docs/eval/semver.md: each confirmed result is a **bug** (a
deadlock, leak, race or panic a caller can trigger through the
documented API, or one the package's own tests trigger), **by design**
(documented, or the caller's misuse the documentation rules out), or
**unreachable**, decided before the trackers are searched; then each bug
is **known** or **new** by the issue tracker (open and closed issues and
pull requests) and the history after the frozen commit. Each spurious or
inconclusive result is a tool issue. Whether to report new bugs upstream
is decided after the run, with the project's owner.

## Results

Not run yet.
