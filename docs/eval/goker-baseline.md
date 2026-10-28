# Evaluation 6: go2lean on GoKer against Go's own tools

go2lean finds and confirms the bug in 60 of GoKer's 68 blocking kernels
and 27 of its 35 non-blocking ones (docs/concurrency.md). That says what
go2lean finds, not what it adds: a kernel's test can trigger its bug, and
Go's scheduler often does on its own. Fuzzing does not vary schedules, so
for concurrency the baseline is what a developer runs: the test, many
times, with the race detector, and a leak checker. This page is the
protocol, committed before the baseline runs; results are added
afterwards and the protocol is not edited once the run starts.

## Kernels

All 103 GoKer kernels of GoBench (Yuan et al., CGO 2021) at `2e91eb1`: 68
blocking, 35 non-blocking. Each kernel's bug is the ground truth.

## Arms

All on the same 4-core machine, one kernel at a time, arms one after the
other.

- **A, go2lean.** `scripts/goker.sh` with the go2lean of the commit that
  adds this page: each kernel's test function as the target (`-tests`),
  default options. Found: a confirmed verdict (the replay deadlocked,
  leaked, panicked or raced in Go). Time: per kernel, from compile to
  verdict.
- **B, the test under the race detector.** The kernel's test binary, built
  with `-race`, run once at a time with `-test.timeout 10s`, again and
  again until it fails or 120 seconds have passed. Found: a race report,
  Go's own deadlock detection ("all goroutines are asleep"), a panic, or a
  run that times out (a hang).
- **B', as B plus goleak.** The kernel's test gets
  `defer goleak.VerifyNone(t)` first, as GoBench's own evaluation
  instruments it (`goker_evaluation.go`). Found: as B, or a leaked
  goroutine.

B' is the strongest dynamic baseline GoBench itself measured; B is what a
project gets from `go test -race` alone.

## Scoring

Per kernel and arm: found or not, and the time to the first finding.

- **Blocking kernels:** a deadlock, a hang, a leak or a misuse panic.
- **Non-blocking kernels:** a race report, or the kernel's panic.

A finding of the other class (a race in a blocking kernel) counts, as in
docs/concurrency.md. Reported:
- kernels found by A only, by B' only, by both, by neither;
- the same against B;
- for A-only kernels, why the baseline misses them (the schedule is rare,
  the leak needs a specific interleaving);
- for B'-only kernels, the model gap.

The claim to test: on concurrency bugs, the model's search over schedules
finds bugs that repeated runs under the race detector and goleak miss in
the same time. If A finds nothing B' misses, that is the result.

## Cost

No LLM. CPU only: A about 2 hours, B and B' at most 103 × 120 s each.
