# Limitations and open problems

What go2lean does not deliver yet, stated against what the design promises.
Written from an outside review (2026-10-21); each item names what exists, the
gap, and the planned work. The README and the design doc should not claim
more than this page allows.

## 1. No proofs in the pipeline yet

The design promises two outputs: a confirmed PoC, or a Lean theorem
(`proved` / `bounded_proved`). What exists:

- One hand-written proof, `lean/Proofs/OobClampIdentity.lean`, for one
  function. The `bv_decide` engine (M1b) is not in the pipeline, and the next
  harder case (`SpecClampIdempotent`) does not go through yet.
- The "exhausted" certificates are computed by `go2lean-search`, compiled Lean
  running the same `denote` interpreter that finds counterexamples. The Lean
  kernel checks none of them. Their trust base is the model's semantics, the
  Lean compiler and the enumeration code.
- `bv_decide` itself checks its LRAT certificate through `ofReduceBool`, so
  it also trusts the compiler. Only proofs by kernel-checked tactics (`omega`,
  `bv_omega`, `simp`, `decide` on tiny domains) avoid that.

So today Lean is a bit-exact interpreter plus a search loop, and all
confidence in a finding comes from `go test`. What Lean adds over the same
model written in Go is a semantics that can also be the subject of a
theorem; that path is not walked yet.

Since 2026-10-22 the pipeline proves part of it (design §5.3, rung 3): for
a `no_panic` property of a function whose parameters are integers and
bools, M2 writes a theorem over every input and records `proved` with its
trust base read from `#print axioms` (`kernel`, or `compiler` when
`Lean.ofReduceBool` appears). On the example packages 7 of the 13 eligible
properties that held are proved, all `kernel`. Everything else is still
what this section says: postconditions, functions taking slices, strings,
pointers or interfaces, and the LLVM line get bounded searches only, and
`exhausted` remains a name for a search, not a proof. The theorem is about
the model; how faithfully the model follows Go is §5's question, which no
proof answers.

Since 2026-10-23 functional properties are proved by hand for a first set
of functions, loops and slice reads included (`lean/Proofs/Functional.lean`,
checked in CI by `scripts/proofs.sh`): `Max2`, `CountUp` and `Max` of
`examples/proofs`, each for every input, kernel only. They rest on
`Go2Lean.Proof.WP`, a partial-correctness weakest precondition read off the
interpreter with a cut-point rule for loops. Still missing: calls (callee
contracts), heap writes, the automation that would let the pipeline write
these proofs (block walking is mechanical; loop invariants are the agent's
part), and termination (the theorems are about runs that end).

## 2. Under-approximations are not tracked mechanically

P1 says the model may over-approximate, never under-approximate.
`docs/semantics.md` lists places where it does under-approximate: a loop over
an unknown map runs zero times, an update to one is dropped, `len` of one is
havoc below 16; writes to globals are dropped, `init` does not run (except the stores it makes on every path, since 2026-10-23), `defer`
of an unmodelled function and calls without results are ignored; two non-nil
errors compare equal; a slice from an unmodelled function has 0 to 3 zero
elements. A spec verdict that read a havoc value is reported as unknown, but
the other assumptions flow silently through a run, and the exhaustive engine
does not enumerate havoc sites.

So an `exhausted` result means: no counterexample on a small finite domain,
havoc sites not enumerated, and the run may have taken an
under-approximating branch. Which assumptions invalidate a given certificate
is not recorded.

Since 2026-10-21 under-approximations are recorded as the run takes them.
The frontend marks each site it lowers to one (a dropped store to a
global or into a handle, an ignored call or `defer`, a map update on an
unknown map, an assumed pointer, slice or zero value for an unmodelled
result, a length kept below 16) with an `assume` instruction; the core
records a callee the model got stuck in (its effects dropped) and calls
through unknown function values or into packages not linked; the GoSSA
dialect records the run-time ones (a `range` over an unknown map that ran
zero times, two unknown non-nil errors compared equal). An exhaustive pass
whose runs took any of them is recorded as `exhausted_assuming`, with the
list, instead of `exhausted`; counterexamples carry the list too. On the
example packages 4 of 101 certificates changed (a store to a global
dropped, a callee the model got stuck in, an assumed `errors.As` target).

Still open: `sync` calls are dropped without a note (no concurrency is
modelled at all, see §7); havoc sites are still sampled, not enumerated
(`exhausted_havoc`); and the list is not yet a hypothesis of any theorem.

## 3. Confirmed is not the same as actionable

M3 shows that the model's counterexample reproduces in Go, not that a real
caller can build that input. In the sixth full pass, 741 of 912 confirmed
results involved a nil input; in the seventh, 231 came from constructors
handed forged third-party structs (a `*gorm.DB` whose internal maps are
nil), a class "handles" (docs/history.md §9b) later removed, one heuristic at a
time. In
the blind-spec experiment, 5 of 39 confirmed results came from wrong specs:
confirmed measures a violated oracle, and sometimes the oracle is the bug.
The 82 bugs published for tingly-box are the result of hand triage; the
tool itself does not rank. `nil_opaque_input` is a first tag, not a ranking.

Since 2026-10-21 every confirmed verdict carries a `rank`
(`internal/poc/rank.go`, `nilflow.go`; `go2lean rank` re-ranks an existing
workspace, `go2lean summary` groups the findings by tier):

| tier | input |
|---|---|
| 1 | plain values any caller of the package's API can pass; a caller in the module can pass the same nil; or every caller passes constants and these are among them |
| 2 | plain values to an unexported function whose callers pass variables, a nil a caller may pass (a field, a global: not traced to a nil), or a hand-written spec violated |
| 3 | a nil no caller in the module can pass, an unexported function whose callers all pass other constants, or an agent-written spec violated |
| 4 | an input the test forged: an opaque value left nil, package state the program may initialise first |

Nil-ness is traced through the callers' SSA (since 2026-10-22): a nil
constant through phis and conversions, a caller's parameter up to three
callers up, the results of functions that return nil; a call reached only
past `v != nil` (a dominating check) is not counted; a nil receiver is also
traced to the places a nil pointer of its type is stored in an interface
(a typed nil). On the 53 confirmed results of an older tingly-box workspace
(`out-tingly6`) this gives 1 / 10 / 42 / 0: 33 are nil receivers of methods
called only through an interface (mostly `Error()`) where no nil of that
type is ever boxed; 5 have call sites none of which can pass nil; 4 are
`truncate(s, n)` helpers whose callers all pass constants such as 30 or
200, so a negative `n` is not reachable; 5 have callers passing a field
whose nil-ness is not traced. The first version (literal `nil` at the call
site only) had marked one finding tier 1 that a dominating `e != nil`
check rules out.

## 4. Evidence comes from one codebase

The heuristics of docs/history.md §9c (documented enums, Stainless SDK `param.Opt` and
`apijson`, SDK unions, GORM handles, lookup tables, Gin contexts) were
written for tingly-box, and the benchmarks (17/17, 31/34) are bugs found by
hand in the same codebase: read, find, write a spec, tune go2lean until it
finds it. one-api and spf13/cast were used only to collect constructs
(`examples/portable`), with no yield reported.

Planned: a blind run with a frozen tool version and a protocol fixed in
advance on codebases of other kinds (a CLI, a pure-Go database, a Kubernetes
controller), with no tool changes during the measurement.

First blind run (2026-10-22, `docs/eval/semver.md`): Masterminds/semver,
protocol committed before the run. Templates alone: 8 confirmed, no bug.
The agent's 21 blind specs: one bug confirmed (bare wildcards with `^`
and `<=`; the `<=` half new), and Go's own fuzzer, given the same specs,
found it plus one more new bug in seconds. One model gap (package
`init()` not run, so regexps compiled there are unknown) made 44 of 53
counterexamples spurious. The value on that package was in the specs,
not in go2lean's search.

## 5. Hand-written semantics; the cross-check

About 2,860 lines of GoSSA dialect and hand reimplementations of strings,
`strconv`, `regexp`, `encoding/json`, `apijson`, `net/url` are checked only
by sampled differential tests (`scripts/json-diff.sh`). Several model bugs
gave wrong answers rather than getting stuck (`RawMessage` HTML escaping,
`omitzero`, `apijson` coercion); each such bug can hide a real one.

The independent check is the TinyGo line (design §4.4): the same package
compiled by TinyGo to LLVM IR, with the standard library and runtime taken
from their real source and run with VeIR's semantics, so it shares none of
the hand-written models. It compares verdicts per function today; it does
not share `ir.Prog`, and it does not yet compare return values on the same
inputs, which is what would catch a model that runs but computes the wrong
value.

First result (2026-10-21, design §4.4): on the 94 exported functions of
the example packages the two lines agree on 73, 29 of them on the same
confirmed bug; none of the 6 differences was the two models computing
different values, and 15 functions the TinyGo line does not model yet
(stuck in `strconv`, `encoding/json`, `regexp`). The examples are small and
were written for the Go line, so this says little about the hand-written
models on real code; running the comparison on tingly-box packages, and
comparing return values, are next.

## 6. M3 runs generated code without a sandbox

The history records `log.Fatal`/`os.Exit` killing a test batch (four
rounds), functions writing files, a 14 GB build cache filling the disk and a
9 GB search killed by the OOM killer. The docs advise a scratch clone; the
tool does not enforce isolation, and the `agent` subcommand has an LLM write
specs that then run in the user's repository.

Since 2026-10-21 (`internal/sandbox`): the harness joins the package
through `go test -overlay` (nothing is written into the package), the test
binary is built with `-c` and runs in a fresh copy of the package
directory under [bubblewrap](https://github.com/containers/bubblewrap)
(`bwrap`, the unprivileged sandbox Flatpak uses: the file system read-only
except that copy, a private `/tmp`, no network, its own PID namespace,
killed with go2lean) and `prlimit` (8 GB address space, 300 s CPU, 256 MB
files, 10 min wall clock). Without a usable `bwrap` (not installed, or
user namespaces disabled) only the limits apply and go2lean says so on
stderr; `GO2LEAN_SANDBOX=off` disables the sandbox. The C and TinyGo
replays use the same runner (ASan runs without the address-space limit).
The build step itself (`go test -c`) still runs cgo and the toolchain
outside the sandbox, as any build does. Not covered: the `agent`
subcommand's own LLM calls, and the M2 search, which runs only the
generated Lean model.

## 7. Smaller items

- Unit tests are thin (7 test files for 35 Go files); the gates are the
  end-to-end regression and differential scripts, which are slow.
- Concurrency is out of scope: one goroutine, `sync` calls ignored, no
  channels or `select` interleaving, so data races, deadlocks and
  ordering bugs can be neither stated nor found. Reports on concurrent code
  should say so prominently. Properties are about one run of a function;
  properties of a sequence of calls over a finite input are written as
  `go2lean:sequence` specs (design §5.2) and checked by bounded model
  checking, which covers "always" after each step and "eventually" at the
  end of a finite stream, not infinite traces or fairness.
- The agent comparison (Opus vs MiniMax-M3) rests on single runs, without
  repetitions, cost accounting or ablations.
- Resource limits are ad hoc (64 KB strings, 8 GB cache sweep, 24 leaves
  for exhaustive search, 64 type-switch cases).
- `docs/design.md` mixed the current design, superseded plans and a
  development log; since 2026-10-21 the log and the old plans are in
  `docs/history.md`. Some sections (5.3, 5.4) still read as a record of
  how they were built.
