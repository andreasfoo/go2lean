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

Run on 2026-10-24 with go2lean `2ab397f`, as the protocol above says, on
a 4-core machine, the arms one after another.

### Summary

| | A: exported API | A': + the packages' tests | C: `go test -race -count=20` |
|---|---|---|---|
| go-resiliency: functions modelled | 41 complete, 19 partial | 64 complete, 49 partial | |
| go-resiliency: properties, counterexamples | 38, 17 | 69, 26 | 5 packages |
| go-resiliency: verdicts | 10 confirmed, 3 spurious, 4 inconclusive | 12 confirmed, 6 spurious, 8 inconclusive | `batcher` fails: 2 race reports |
| go-resiliency: time | 90 s | 167 s | 7 s |
| go-cache: functions modelled | 111 complete, 48 partial, 7 rejected | 115 complete, 119 partial, 7 rejected | |
| go-cache: properties, counterexamples | 75, 16 | 148, none: the model did not build (tool issue 1) | 1 package |
| go-cache: verdicts | 16 confirmed | none; with the fix: the same 16 confirmed | passes |
| go-cache: time | 75 s | 4830 s (the failed Lean build); with the fix, 120 s | 6 s |

**Bugs: 1, new, found by A' and C alike.** `TestShutdownSuccess` (go-resiliency
`batcher`) races on its own `total`: its `doWork` (`doSum`) adds to a local
without a lock, while the package documents that `doWork` must be safe to
run concurrently with itself (two batches can overlap). go2lean found it in
the model (`data_race`: one goroutine reads the variable another wrote,
neither ordered before the other) and the replay stage confirmed it under `-race`, with the model's schedule
replayed (the replay diverged at step 39 of 104 and Go's run raced anyway).
C reports the same race and a second one in the same test, `Add` in
`submitWork` against `Wait` in `Shutdown`: the test calls `Run` while
`Shutdown(true)` runs, which the package documents as undefined behaviour,
so it is the test's misuse; go2lean does not report it (a `WaitGroup`'s
`Add` concurrent with a `Wait` at zero is not a race in the model). The
tracker has a closed batcher race (issue 46, fixed by PR 47, which also
turned race detection on in CI) on other code, and nothing on this test.

### Triage of the confirmed results

| result | arms | triage |
|---|---|---|
| `TestShutdownSuccess`: data race on `total` | A' (C too) | **bug** in the package's test, new |
| `semaphore.New` with a negative ticket count: `make` panics | A, A' | by design: an invalid argument, as Go's `make` |
| `Semaphore.Release` without an `Acquire`: blocks forever | A, A' | by design: documented as an error |
| `retrier.ConstantBackoff`, `ExponentialBackoff`, `LimitedExponentialBackoff` with a negative count: `make` panics | A, A' | by design: an invalid argument |
| `Batcher.Run`, `Breaker.Run`, `Breaker.doWork`, `Deadline.Run` with a nil function or receiver: nil dereference | A, A' (`Deadline.Run` A' only) | by design: a nil argument |
| `Batcher.batch`, `Retrier.sleep` called directly: block forever | A, A' | unreachable: internal, called with a channel nobody closes and a timer that never fires |
| go-cache: `newShardedCache`, `unexportedNewSharded` and 12 `shardedCache` methods with a negative shard count: `make` panics | A, A' | unreachable: the sharded cache is unexported, and only the package's tests build it (with 10 to 20 shards) |
| go-cache: `stopJanitor`, `stopShardedJanitor` without a janitor: block forever | A, A' | unreachable: internal, called by a finalizer only when a janitor runs |

### Tool issues

1. **A string table broke the model** (go-cache, A'): `var shardedKeys =
   []string{...}` in a test file made the frontend emit string literals as
   constant values, which Lean rejects; the whole package went unmodelled.
   Older than C2, never hit before (no example had a package-level table of
   strings). Fixed after the run: a slice the initializer builds from
   constants allocates its strings at the load, a constant array table
   holds none; `examples/initvars` has `Unit`.
2. **A failed model build did not fail the run**: `go2lean run` printed
   the Lean errors and exited 0. Fixed: the sweep goes on, and the run
   exits non-zero naming the packages that failed.
3. **Method values passed to `time.AfterFunc` are not followed**:
   `time.AfterFunc(d, b.flushCurrentBatch)` spawns a goroutine on the
   method value's wrapper (`flushCurrentBatch$bound`), which the model
   does not include, so the flush never runs and four of the batcher's
   tests deadlock in the model; the replay stage left them `inconclusive`
   (the schedule did not reproduce). Not fixed yet.
4. **Results of unmodelled calls**: `time.Now` (four breaker tests) and
   `(*time.Timer).Stop` (`Batcher.submitWork`) are havoc, and the model
   picks values Go does not give; all five refuted by the replay stage
   (spurious).
5. **Tests stuck on a field of an unmodelled value**: 17 of go-cache's
   tests and 10 of go-resiliency's stop at a field access on a value the
   model does not build (the `Cache` wrapper `New` returns, with its
   finalizer and janitor), so they are not explored.
6. **`Deadline.Run` with a nil function is spurious in A and confirmed in
   A'**: the goroutine's panic races `Run`'s own timeout, and one real run
   is not always enough to see it.
7. **The retrier's `Run`, `RunCtx`, `RunFn` and `calcSleep` are
   inconclusive** (`zero_handle_input`): the harness passes a zero
   `Retrier` the package never builds.

### Reading

On concurrent code the model reaches the bugs a test or a caller can
trigger, and the replay stage holds: every confirmed concurrency result
is real Go behaviour, and every spurious or inconclusive one is a tool
issue above, not a wrong verdict. But on these two libraries the
concurrency bugs that exist are in the tests, and `go test -race`
already finds them in 7 seconds; go2lean's own contribution here is the
argument and API-misuse panics, all by design or unreachable. The run
also found two frontend bugs (1, 2) that no earlier target hit, and
three model gaps (3 to 5) worth closing before the next concurrent
target.
