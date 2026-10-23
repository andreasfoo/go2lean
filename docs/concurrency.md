# Concurrency in the model (C1)

Status, 2026-10-23: the machine, the frontend lowering and the M3 checks
are built (C1a), and `context`, timers and `sync.Cond` (C1b);
deterministic replay is next. "Built" below lists what landed and how it differs from the
plan; the rest of the page is the plan. What other
projects offer for it (Goose's channel model, Veil, Iris-Lean) is in
[related-work.md](related-work.md).

## Built (C1a)

- `Go2Lean.GoConc`: the small-step machine below; `Term.spawn` and
  `Term.sync` in the core; `SyncOp` covers channels (send, receive with or
  without `ok`, close, `select`), `Mutex`, `RWMutex`, `WaitGroup`, `Once`
  and a yield (`time.Sleep`, `runtime.Gosched`). The search engines run a
  function in the machine when it can synchronise (`Func.sync`), else as
  before. Semantics by construct: [semantics.md §6](semantics.md#6-concurrency).
- Frontend: `go`, channel operations, `select`, `close`, `len`/`cap` of a
  channel, the `sync` methods (also deferred), `sync/atomic`'s functions,
  method values (`go x.run()`), direction-only channel conversions.
  Package variables holding sync state with no initializer (`var mu
  sync.Mutex`, a struct embedding one) are cells every goroutine shares
  (`globalAddr`).
- End of a run: `deadlock`, `goroutine_leak` (not for a goroutine blocked
  on a channel the function returns), and the panics `closed_channel`,
  `sync_fatal`; the report names where each goroutine waits.
- Targets: `-tests` makes `TestXxx(t *testing.T)` targets, as GoKer's
  kernels are written; `t` is the harness's own in M3.
- M3: a deadlock or leak runs the call up to 200 times under a 2-second
  watchdog, a leak is a goroutine of the package blocked on a channel or
  lock after the call returned (not one alive before); a panic in a
  goroutine the call started ends the test binary and is judged from Go's
  report on stderr.
- Not as planned: there is no separate `no_block` property; `no_panic`
  covers deadlocks and leaks (they end the run like a panic). Fuel counts
  goroutine steps, and the schedule is one havoc site.
- Ground truth: `examples/conc` (6 planted bugs in `conc.go`, 3 more with contexts, timers and a condition variable in `timeouts.go`);
  `scripts/goker.sh` runs GoKer.

## Built (C1b)

- `context`: a context is a heap object (done channel, error, children);
  `WithCancel`, `WithTimeout` and `WithDeadline` make a child, the cancel
  func is a synthetic function of the program (`$context.cancel`) that
  closes the done channel and its children's, and a timeout or deadline is
  a goroutine that cancels at any scheduling point (`$context.deadline`):
  the deadline may pass at any time.
- Timers: `time.After` and `NewTimer` are a one-value channel and a
  goroutine that sends on it at any point without blocking (`$time.fire`);
  `NewTicker` a daemon goroutine (`$time.tick`, `Func.daemon`) that does
  not keep a run alive; `AfterFunc` a goroutine running the function.
- `sync.Cond`: a ticket queue in the cond's `notify` field (`condEnter`,
  `condSignal`, `SyncOp.condWait`); `Wait` unlocks and relocks `L` through
  `sync.Locker`, whose `Lock`/`Unlock` dispatch on the boxed type (a
  `*Mutex`, a `*RWMutex`'s writer side, its `RLocker()`'s reader side).
- Also: deferred calls through func values (`defer cancel()`), `go` of an
  interface method, `go close(ch)`; wrappers around synchronising methods
  (`T.M~NewT`) now get frames; the writer-preference rule blocks new
  readers only while a writer waits for readers to leave (a writer that
  could take the lock is not waiting yet); the M3 leak scan waits while a
  goroutine of the package is still running or sleeping.

### GoKer

`scripts/goker.sh <gobench> <out>` on all 103 kernels (GoBench at
2e91eb1), each kernel's test function as the target, default search
budget, 2026-10-23, with C1b. The kernels are the ground truth: each
blocking kernel has a blocking bug its test can trigger; most non-blocking
ones have a data race, which C1 does not model.

| | blocking (68) | non-blocking (35) |
|---|---|---|
| bug found by the model, confirmed by `go test` | **53** (16 deadlocks, 36 leaks, 1 double unlock) | 6 (3 leaks, 2 `closed_channel`, 1 nil dereference) |
| found by the model, not reproduced by `go test` in 200 runs (`schedule_not_reproduced`, one `panic_kind_mismatch`) | 6 | 1 |
| found by the model, refuted by `go test` (spurious) | 1 | 4 |
| nothing found | 8 | 24 (holds) |

With C1a alone (no `context`, timers or `Cond`) the first row was 35 and
16 blocking kernels got stuck on a channel the model did not know. The
model now reports a blocking bug in 60 of the 68 blocking kernels; `go
test` confirms 53 (a confirmed finding is the real kernel deadlocking,
leaking or panicking, not the model).

- Not reproduced (cockroach 1462, 7504, 10214, 16167, istio 16224,
  serving 2137; in an earlier run of the same model also etcd 5509 and
  kubernetes 1321, 62464, which this run confirmed): the model's schedule
  is one Go's scheduler rarely takes; deterministic replay (C1c) is for
  these.
- Not found: `fmt` calling a `String` method that takes a read lock
  (cockroach 6181), `io.ReadFull` (grpc 1275), `cond.Wait` passed as a
  func value (kubernetes 11298), a sync-holding package variable set by
  `init` (moby 29733), an external test package `package x_test`
  (syncthing 5795), and three whose trials all run out of fuel (grpc 862,
  istio 18454, kubernetes 58107: polling loops).
- Spurious, each rejected by M3: `recover` (etcd 7443 recovers its send on
  a closed channel), a store to an `int` package variable ignored (etcd
  8194), unmodelled `os/exec` and a callback (moby 18412, serving 6472),
  and a data race the model's sequentially consistent steps turn into a
  panic (kubernetes 13058).

The split between confirmed and not reproduced moves a little between
runs: Go's scheduler varies from run to run.

## Why

go2lean's properties so far need a spec, except `no_panic`. Concurrency
gives spec-free properties that matter: a test or a function that spawns
goroutines must not deadlock, must not leave goroutines blocked forever
(a leak), and must not panic from misusing a channel or a lock. Existing
Go tools split the space:

- `go test -race` finds data races in the interleavings a run happens to
  take; it says nothing about blocking bugs.
- Static checkers (GCatch, Gomela, dingo-hunter) model channel and lock
  operations but abstract data away, so a blocking bug that depends on a
  value (a full buffer, an error path that skips an `Unlock` or a send)
  is missed or misreported.

go2lean already models the data: the program's own code, values and heap.
Adding goroutines and synchronisation to that model lets the search vary
inputs and schedules together, and lets the exhaustive engine state
"no deadlock, no leak, no panic under every schedule" for a bounded
configuration: a certificate neither `-race` nor fuzzing gives.

Ground truth: GoKer, the kernel half of GoBench (Yuan et al., CGO 2021):
103 small programs (median 86 lines) extracted from real concurrency bugs
in Kubernetes, etcd, gRPC, Docker, CockroachDB and others, 68 blocking
and 35 non-blocking, each with a test that can trigger the bug. 31 of
the blocking and 16 of the non-blocking kernels use only goroutines,
channels, `select`, `Mutex`, `RWMutex`, `WaitGroup` and `Once`; most of
the rest add `time` and `context`.

## Scope of C1

| Go | model |
|---|---|
| `go f(x)`, `go func(){...}()` | a new goroutine, runnable |
| `make(chan T)`, `make(chan T, n)` | a channel object on the heap: capacity, buffer, closed flag |
| `ch <- v`, `<-ch`, `v, ok := <-ch`, `close(ch)`, `len(ch)`, `cap(ch)` | Go's semantics: unbuffered is a rendezvous, a send on a closed channel and a close of a closed or nil channel panic, a receive on a closed channel gets the zero value and `ok == false`, a nil channel blocks forever |
| `for v := range ch` | receives until closed |
| `select` with send and receive cases, with or without `default` | one enabled case, chosen by the schedule; blocks when none is enabled and there is no default |
| `sync.Mutex` `Lock`, `Unlock`, `TryLock` | a lock word in place of the field; `Unlock` of an unlocked mutex is a fatal error (reported as a panic) |
| `sync.RWMutex` | writer preference, as Go: a blocked `Lock` stops new `RLock`s (the classic read–write–read deadlock) |
| `sync.WaitGroup` `Add`, `Done`, `Wait` | a counter; negative is a panic; `Wait` blocks until zero |
| `sync.Once` `Do` | done / running / not started; a second `Do` blocks while the first runs |
| `time.Sleep`, `runtime.Gosched` | a scheduling point (any goroutine may run next); time itself is not modelled |

Later in C1: `sync.Cond`; `time.After`, `NewTimer`, `NewTicker` (a
channel a timer goroutine may send on at any scheduling point);
`context.WithCancel`, `WithTimeout` (a done channel closed by `cancel` or
by a timer goroutine). Out of C1: data races (C2: happens-before in the
model), `sync/atomic` ordering, `runtime.NumGoroutine`.

## The machine

The existing interpreter (`Go2Lean.SSA.runFunc`) is big-step: a call runs
to its end. It stays as it is, for sequential code and for the prover.
Concurrent code runs in a small-step machine (`Go2Lean.SSA.Conc`):

- **State**: the heap (channels and lock words live there), the
  goroutines, and the havoc state. A goroutine is a stack of frames
  (function, block, environment, where the caller continues) and a
  status: runnable, blocked at a synchronisation term, or done.
- **Atomic steps**: a goroutine runs from one scheduling point to the
  next. Instructions and calls to functions that cannot reach a
  synchronisation operation (the frontend marks them) run with the
  big-step interpreter, atomically. Calls to functions that can push a
  frame.
- **Scheduling points**: every synchronisation term (`go`, channel
  operations, `select`, lock and wait-group operations, `Once.Do`,
  `Sleep`). At each, the schedule picks which runnable goroutine moves
  next: the next value of a dedicated havoc site, modulo the number of
  enabled goroutines. So a schedule is part of a trial's input, the
  random engine varies it, a counterexample records it, and the
  exhaustive engine enumerates it for small bounds.
- **Blocking**: a goroutine at a term that cannot fire (a receive on an
  empty channel, a `Lock` of a held mutex) is blocked; it is enabled
  again when the term can fire. An unbuffered send blocks offering its
  value; a receive (or a `select` case) takes an offer and completes the
  sender's term.
- **End of a run**: when the main goroutine (the function under test)
  returns, the others run until none is enabled. Then:
  - all done: the run returns the main goroutine's result;
  - some blocked: **leak** (a goroutine blocked forever);
  - the main goroutine blocked and none enabled: **deadlock** (Go's
    "all goroutines are asleep").
  A panic in any goroutine ends the run with that panic, as in Go.

Fuel counts steps across all goroutines, so an unfair schedule that spins
one goroutine runs out of fuel instead of looping (reported as such, not
as a finding).

## Frontend

- `go`, channel operations, `select`, and calls of the `sync` methods
  above lower to synchronisation terms instead of being rejected.
- Each function is classified: it *can synchronise* when it, or a
  function it calls (statically, through a known func value, or by
  interface dispatch to a modelled method), performs one. Only those get
  frames in the machine.
- Lock words: a `sync.Mutex` field is a heap slot like any field; its
  zero value (today an opaque value) reads as unlocked.

## Properties and targets

- `no_block`: the function returns, and when it does, no goroutine it
  started is blocked (no deadlock, no leak). Generated for every function
  that can synchronise, like `no_panic`, and for test functions
  (`TestXxx(t *testing.T)`, `t` unused or used only through the methods
  the fuzz-target import already rewrites), which is how GoKer's kernels
  are written.
- `no_panic` on such functions now covers the panics of misusing a
  channel, a mutex or a wait group.

## Checking against Go (M3)

Go's scheduler cannot be told what to run next, so a model schedule
cannot be replayed directly. Two steps:

1. First: run the target under `go test` many times (with `-race`, and
   with `GOMAXPROCS` varied) and report how often the deadlock, leak or
   panic shows up; a leak is detected with a goroutine dump at the end of
   the test. A finding that never shows up stays "model only", reported
   with its schedule.
2. Then: deterministic replay. The overlay mechanism the fuzz-target
   import uses rewrites the target's source so that every
   synchronisation operation first waits for a token from a replay
   controller that follows the model's schedule. The real program then
   takes the model's interleaving.

## Measures

On GoKer: kernels modelled (all their functions complete), blocking bugs
found by the model, confirmed by `go test`, found with or without the
deterministic replay, time; non-blocking kernels are C2's. Fixed versions
of the kernels (from the linked patches) measure false positives.
