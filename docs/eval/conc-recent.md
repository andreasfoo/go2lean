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
