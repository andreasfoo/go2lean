# Evaluation 7: recent concurrency bugs, go2lean against Go's own tools

Issue #37 asks what the Lean model adds over simpler ways of finding
defects. For functional defects, Go's fuzzer did as well or better on the
same specs (docs/eval/search-vs-fuzz.md). Fuzzing does not vary schedules,
so concurrency is where a model that enumerates schedules could add
something. GoKer (docs/concurrency.md) is small and from 2021; this run
uses concurrency bugs fixed since 2023 in projects people use now. This
page is the protocol, committed before any case is run; results are added
afterwards and the protocol is not edited once the run starts.

## Cases

- **Projects:** 22 actively maintained Go projects that use concurrency
  heavily, history since 2023-01-01 (`conc-recent/mine.py`):
  - grpc-go, etcd, nats-server, nats.go, pgx, go-sql-driver/mysql;
  - kafka-go, sarama, amqp091-go, quic-go, ants, pond;
  - hashicorp/raft, yamux, memberlist, badger, pebble;
  - go-redis, containerd, caddy, prometheus, sourcegraph/conc.
- **Pool.** Commits whose subject names a race, a deadlock, a leak, a hang
  or a synchronisation primitive and reads like a fix, that change 1 to 5
  non-test Go files and a test: 264 candidates. At most 5 per project are
  taken, in a seeded order (`conc-recent/draw.py`, seed 20261028), giving
  70 (`conc-recent/pool.jsonl`).
- **Screening.** Read in that order, until 25 are accepted. A case is
  accepted when:
  - the fix removes a concurrency defect in non-test code: a data race, a
    deadlock, a goroutine leak, a lost wake-up or hang, a misuse panic
    (closing a closed channel, a negative `WaitGroup`, unlocking an
    unlocked mutex), or an atomicity violation with a wrong result;
  - one test function of the package exercises it (the fix's own test, or
    an existing one) and runs without external services (databases,
    brokers, containers). In-process loopback networking is allowed and
    recorded.
- **Ground truth for each accepted case:**
  - **Observed:** at the parent commit the test fails because of the defect
    at least once in 200 runs or 10 minutes, under `-race` and with goleak
    for a leak, and at the fix it does not fail in the same budget.
  - **Forced:** when the parent's test never fails in that budget, the
    screener writes a private reproduction that forces the interleaving
    (channels, sleeps or hooks in a test). The case is accepted as forced.
    These are where a schedule search could find what repetition does not.

  Every rejection is recorded with its reason.

Neither arm writes specs, so no blinding is needed: the target is the test
function, and the property is that it does not deadlock, leak, race or
panic.

## Arms

All on the same 4-core machine, one case at a time, arms one after the
other.

- **A, go2lean.** `go2lean run -tests -run '^<Test>$' -budget 300` on the
  test's package, at the parent commit, with the go2lean of the commit that
  adds this page. Found: a confirmed verdict whose kind (deadlock, leak,
  race, panic) is the defect's.
- **B, Go's tools.** The test binary built with `-race`, the test function
  starting with `defer goleak.VerifyNone(t)` (as GoBench instruments it),
  run once at a time with `-test.timeout 30s`, until it fails or 10 minutes
  have passed. Found: a failure of the defect's kind: a race report, a
  leak, a hang, Go's deadlock detection, or the panic.

B gets more time than A's budget: it is the strongest baseline a project
has without new tools.

## Scoring

A finding counts when it does not occur at the fix commit:
- for A, the counterexample is replayed there (`go2lean poc`);
- for B, the test is run there again for the same budget.

Per case and arm: found or not, and the time to the first finding.
Reported:
- found by A only, by B only, by both, by neither;
- the same split for observed and forced cases;
- for A's misses, the model gap (`go2lean-spec/scripts/gaps.go`);
- for B's misses, how rare the schedule is (the forced cases).

The claim to test: the model's search over schedules finds defects that
repeated runs under the race detector and goleak do not find in 10
minutes. If A finds nothing that B misses, that is the result, and the
README will say so.

## Cost

The screening agent: one run, about 0.4 to 0.6 M tokens, and CPU time to
rerun tests. Neither arm calls an LLM.

## Amendment before the run (2026-10-29)

Screening accepted 25 cases (22 observed, 3 forced). In 22 of them the
fix's own test fails at the parent in 90% or more of its runs under
`-race`: maintainers write a regression test to reproduce the bug
reliably. Run against that test, arm B would find nearly every case on its
first run, and the evaluation would measure the test, not the tools. The
arms are changed before either has run, to ask what matters in use: can a
tool find the defect before anyone wrote a test for it?

- Both arms run at the parent commit **without** the fix's test files and
  without the screener's tests (`custom/`).
- **A, go2lean:** `go2lean run -tests -budget 60` on the package: its
  existing tests and the `no_panic` templates of its exported functions
  and methods, with a 20-minute limit per case.
- **B, Go's tools:** the package's existing tests, `go test -race
  -count=1 -timeout 5m`, again and again until a failure or 10 minutes.
- **Found:** a finding (a race report, a deadlock, a leak, a hang or a
  panic) whose stacks name a function the fix changes, and which does not
  occur at the fix commit. Each match is checked by reading the report.
- The fix's test (or the screener's reproduction) stays the ground truth:
  it shows the defect is real.

The cases with in-process loopback networking (6) are reported apart.

## Second amendment before the run (2026-10-29)

The first amended design was started on 7 cases (k001 to k014) and
stopped. Neither arm found a defect, for reasons that would hold for the
other cases too:
- `go2lean run -tests` did not finish on these packages: it hit the
  20-minute limit in most cases and ran out of memory in one (#45). Its
  `no_panic` templates call one function at a time, so they never run two
  methods of one object concurrently.
- The packages' existing tests had passed the projects' CI with the
  defect present; repeating them under `-race` failed only on tests
  unrelated to the fix (an environment-dependent proxy test, a flaky
  timing test).

So that run measured whether a test exercising the defect exists, not
what either search adds. The design now follows evaluation 5: both arms
search the same program, and only the search differs.

- **Harness.** For each case, one `go2lean:sequence` function in a test
  file of the package at the parent commit (`conc-recent/harness/`): it
  builds one object the way a user would, runs two of its methods
  concurrently (three where the defect needs three parties), takes small
  ints, strings or bytes as inputs, and returns whether the result is one
  some sequential order of the calls could give. It has no sleeps, yields
  or hooks, and nothing of the fix. Harnesses are written by subagents
  following `conc-recent/harness/BRIEF.md`, then reviewed.
- **Not blind.** The writer reads the fix to choose which methods to run
  together, so the harness points at the defect's area. Both arms get the
  same harness; what is measured is the search over inputs and
  schedules, not finding the area.
- **A, go2lean:** `go2lean run -deps -specs-only -budget 300` on the
  harness's package, with the frozen tool. Found: a confirmed
  counterexample (a race, a deadlock, a panic or a false result). A
  counterexample the replay did not reproduce (spurious) is not a find; it
  is counted apart, with its kind, since the defect may still be real.
- **B, Go's fuzzer:** `go2lean fuzz -fuzztime 10m` on the property A
  recorded (the same harness), with `GOFLAGS=-race`. Found: a failing
  input. Go's fuzzer does not check for leaked goroutines, so B finds a
  leak only if it makes the run fail.
- **Scoring** (`conc-recent/score8.sh`). Each arm's failing input is
  tried at the fix commit with the same harness: A's counterexample is
  replayed with `go2lean poc` (repeated `-race` runs and the model's
  schedule), B's failing input is run 20 times under `-race`. It counts
  when it does not fail there and the parent's report is of the defect's
  kind, checked by reading it. The runner is `conc-recent/run8.sh`; the 25
  harnesses, each with its sanity results in `meta.json`, are in
  `conc-recent/harness/` (as `.go.txt`).
- **Sanity runs.** Each harness was run 200 times at the parent and at the
  fix while it was written. A harness that fails at the fix is reworked or
  dropped. A harness that already fails at the parent in a plain loop shows
  the defect needs no search; such cases are reported apart, since either
  arm will find them.
- **Pilot.** The runner was tried on k024 and k030 with a 30-second budget
  and 20 seconds of fuzzing, to check that both arms run; it showed that
  `-deps` and the import path were needed. Both cases are rerun with the
  full settings.
- Cases with loopback networking or real time are reported apart, as
  before.
