# Blind evaluation 3: `go2lean prove` on real code

The first run of `go2lean prove` on code go2lean was not built on: so far
it has proved only functions written for it (`examples/*`). This page is
the protocol. It is committed before `prove` runs on any target; the
results section is added afterwards, and the protocol is not edited after
the run starts.

## Targets

Three small, widely used Go modules of plain functions over strings and
integers:

| module | commit | lines (non-test) | what it is |
|---|---|---|---|
| [lithammer/fuzzysearch](https://github.com/lithammer/fuzzysearch) | `36fdf3c` | 337 | fuzzy string matching and ranking (`fuzzy`) |
| [agnivade/levenshtein](https://github.com/agnivade/levenshtein) | `c4a79bf` | 118 | edit distance |
| [xrash/smetrics](https://github.com/xrash/smetrics) | `39c47d9` | 546 | string metrics: Soundex, Wagner–Fischer, Ukkonen, ... |

**How the targets were chosen.**
- Of nine modules surveyed, these have the most exported functions inside
  what `prove` states: non-methods whose parameters are integers, bools,
  strings or integer slices, with at most one result of those kinds.
- The survey was a mechanical filter on signatures (`go/types`).
- Nobody involved has read their function bodies, issue trackers or
  changelogs.

**The functions in scope.** All 13 that pass the filter:
- `fuzzy`: `Match`, `MatchFold`, `MatchNormalized`,
  `MatchNormalizedFold`, `RankMatch`, `RankMatchFold`,
  `RankMatchNormalized`, `RankMatchNormalizedFold`,
  `LevenshteinDistance`.
- `levenshtein`: `ComputeDistance`.
- `smetrics`: `Soundex`, `Ukkonen`, `WagnerFischer`.

A Lean postcondition about a string result has to go through the heap; a
Go spec takes it as a string.

## Tool

**The build.** go2lean is frozen at the commit that adds this page (Go
1.26, Lean v4.35.0), built once into a separate binary.

**The agent.** The proof agent is the claude CLI's default model
(`-llm claude-code`), with:
- Lean tools (`-lean-tools`);
- `-rounds 6`;
- `-budget 10m`;
- the judge at its default (`lake comparator --paranoid`).

**Tool issues.** Model gaps, crashes and wrong verdicts are logged as tool
issues and fixed afterwards. A rerun after fixes is reported separately,
never merged with the frozen run's numbers.

## Arms

**A. No panic.** For each function in scope, a goal with postcondition
`True`. The theorem then says: for every input, every value the model does
not know and every run that ends, no panic and no under-approximation.
- Loop-free functions are proved by `wp_auto` alone.
- A function with loops goes to the proof agent.

**B. Documented contracts.**

1. **Specs from the docs alone.** A fresh subagent sees only `go doc -all`
   of each package: doc comments and signatures, no function bodies. It
   writes Go specs (`go2lean:spec`) for what the documentation promises.
   It has no web access, no files and no tools that run code.
2. **The search first.** `go2lean run -deps` searches every spec on a
   scratch clone. A confirmed counterexample is triaged as in
   `docs/eval/semver.md`: a bug, or a spec that misreads the docs. Either
   way it is not sent to `prove`.
3. **Then the proof.** Every spec with no counterexample goes to
   `go2lean prove`, with the same settings as arm A.

## Measures

**Per goal:**
- the outcome:
  - proved, with the judge's certificate;
  - or open, with the reason as one of:
    - (a) the function, or one it calls, is not modelled completely;
    - (b) the statement is not supported (a parameter or result kind);
    - (c) the property is false (a confirmed counterexample);
    - (d) the agent found no proof within its rounds;
    - (e) a tool error;
- the agent's rounds, time and tool turns.

**Per proved goal:** the property run as a Go fuzz target for 60 seconds
on the real code. A counterexample would be a soundness failure, and is
reported first.

**Overall:** the fraction proved per arm, and the reasons the rest stay
open.

## Expectations, written before the run

- `fuzzy`'s `Normalized` variants go through `golang.org/x/text`'s
  `transform` and `unicode/norm`, which go2lean does not model, so they
  stay open with reason (a).
- The edit distances are nested loops over `[]rune` with a row slice. They
  are the hardest proofs here and the most informative.
- `Soundex` returns a string and works over bytes. Its `no_panic` is in
  reach.

## Results

The run took 2026-10-25, with go2lean frozen at `6a7312a`. The raw
material is in `prove-blind/`:

- the specs (`*_specs.go.txt`);
- every arm A goal with its rounds, agent time and tool turns
  (`armA_goals.txt`);
- the agent's last answer on each function's goal
  (`armA_last_answers.md`).

### Headline

None of the 12 functions attempted got its `no_panic` contract proved.
The agent did not run out of ideas. On each one it ended by reporting the
statement **false in the model**, with a run of the model that shows it:

| why the contract is false | functions |
|---|---|
| **the real code panics**: integer division by zero when a cost is 0 | `Ukkonen` |
| **havoc values are untyped**. `Havoc Val` may return any `Val`: `.unit` where Go has an integer, or a rune width of 100. The model then takes panics Go cannot take. `Prog.havocWidths` exists but nothing reads it. | `ComputeDistance`, `MatchFold`, `RankMatch`, `RankMatchFold` |
| **every run takes an under-approximation**. Even a harmless one sets `s.assumed`: `Soundex` ignores `strings.Builder.Grow`, which has no observable effect, and the normalising transformer loads the unmodelled `unicode.Mn` | `Soundex`, `MatchNormalized`, `MatchNormalizedFold`, `RankMatchNormalized`, `RankMatchNormalizedFold` |
| **the model's size bound counts as a panic**. `makeSlice` and `append` stop with `modelStuck` above a capacity of 1024, and `wpFun` treats that like a Go panic | `LevenshteinDistance`, `WagnerFischer` |

We checked the size bound (`GoSSA.lean`: `newCap > 1024`,
`cap.toNat ≤ 1024`) and the unused `havocWidths` in the source. The
other diagnoses are the agent's, each with the model run it reports.

So the proof engine on real code is blocked by **the statement**, not by
the prover:

- One function in twelve is wrong in Go.
- The other eleven are right in Go, as far as the search and the docs
  show. They are "wrong" only because the contract counts the model's own
  over-approximations (untyped havoc, a harmless assumption, its size
  bound) as failures.

The P0 change that quantified over every havoc function made the
contract sound. On real code it also made it unprovable in this way. The
examples written for go2lean never call anything unmodelled, so they did
not show this.

### Arm A: no panic

- **Goals:** 42 in all:
  - 12 functions' contracts;
  - their nil-string companions;
  - `MatchFold`'s, stopped partway (below).
- **Proved: 7**, all companions. Every one is accepted by
  `lake comparator --paranoid`.
  - `ComputeDistance` with `a`, `b`, or both empty;
  - `RankMatch` with an empty target, or both empty;
  - `WagnerFischer` with `b`, or both empty.
- **Fuzzing:** each proved property ran as a Go fuzz target for 60 s on
  the real code. No panic.
- **Rejected by the judge after Lean accepted them: 4 companions**
  (`LevenshteinDistance` ×3, `WagnerFischer` with `a` empty). Their
  `wp_auto` proofs ran into the heartbeat limit in the judge's one-goal-
  at-a-time pass, where earlier goals are `sorry`. The workspace accepted
  them; the judge did not. This only makes the judge stricter, not
  unsound. Tool issue 5.
- **Cost:** 191 agent rounds, about 13 hours of agent time, up to 105
  tool turns on one goal.
- **Not attempted: `Match`.** Tool issue 1.

### Arm B: documented contracts

**The specs.** A fresh subagent wrote 40 specs from `go doc` alone.

**The search** (`go2lean run -deps`):

- **A real bug, found by the `no_panic` template.**
  `smetrics.Ukkonen("3", "caa-B \n9", 7, 0, 6)` panics with an integer
  divide by zero. A zero deletion cost is a cost the docs allow. go2lean
  confirmed it by replaying it on the real Go code, and it is the same
  bug the proof agent found from the other side. Not reported upstream
  yet.
- **Two specs falsified, both misreadings.**
  `SpecWagnerFischerNonNegative` and `SpecUkkonenNonNegative` fail only
  at costs near 2^63, where the sum wraps. The spec capped neither the
  costs nor the lengths.
- **No counterexample for the other 38.** Levenshtein's six are
  exhausted; Soundex's four are `exhausted_assuming`.

**The proofs.**

- **The protocol's run failed.** It sent the 38 to `prove` one package at
  a time. All three runs failed before the agent started:
  - two hit the 10-minute budget on Lean's first check of the whole
    file;
  - one ran out of memory.

  That is tool issue 2.
- **The rest were not sent.** A spec's contract includes the function's
  `no_panic` (`wpFun` counts a panic as false), and arm A showed that
  contract false in the model for every function here. So no spec
  contract can be proved either. We stopped arm B's proofs there. This
  is a deviation from the protocol, stated here.

### Deviations from the protocol

- **The spec writer's input.** It saw the docs of the functions in scope
  only, not all of `go doc -all`, and wrote no specs for the
  `Normalized` variants.
- **`Match` was left out of both arms (tool issue 1).** The other
  fuzzysearch functions ran one `prove` per function, since a package-
  wide run stops at the first missing target.
- **`MatchFold`'s companions are incomplete.** Its run was stopped by
  the harness's two-hour limit after the contract (6 rounds, open) and
  one companion. The other functions ran detached, under an eight-hour
  box, which they finished within.
- **`WagnerFischer`** was run on its own, after the smetrics run was
  stopped by the same limit.
- **Arm B's proofs** stopped as described above.

### Expectations against the outcome

- **The `Normalized` variants stay open: yes.** The cause is not that
  `x/text` is unmodelled but the assumptions recorded where it is.
- **The edit distances are the hardest proofs: not tested.** They are
  blocked by the model's capacity bound before any invariant matters.
- **`Soundex`'s `no_panic` is in reach: no.** A harmless ignored
  `Grow` blocks it.

### Tool issues (to fix after the run)

1. **Target IDs.** Two functions whose names differ only in case
   (`Match`, `match`) get suffixed target IDs, and `prove` looks the
   plain one up and stops the whole package.
2. **The first check.** `prove` checks every goal of a package in one
   Lean file before the agent starts. With many goals that file times out
   or runs out of memory.
3. **No `-deps`.** `prove` has no `-deps`, so standard-library functions
   the search models (`unicode/utf8`) are havoc in the theorem.
4. **Companions.** Each nil or empty companion goes to the agent for the
   full rounds. On string functions that multiplies the cost by four,
   and most companions fail for the main goal's reason.
5. **The judge's second pass.** It can reject an automatic proof the
   workspace accepted, through the heartbeat limit once earlier goals
   are `sorry`.
6. **The contract counts the model's own over-approximations as
   failures.** The fix is in the statement and the model:
   - havoc values constrained to the type the site has (`havocWidths`, or
     a typed havoc);
   - assumptions that cannot change Go's behaviour (an ignored `Grow`)
     not recorded as under-approximations;
   - `modelStuck` from the model's own bounds treated like running out
     of fuel, not like a Go panic.

### After the run: the fixes

Added after the results, which are unchanged. The tool issues are fixed
since:

1. Target IDs use the compile stage's keys (`Match`, `match_c0`).
2. Each goal is checked in a file of its own, the earlier goals assumed.
3. `prove -deps` (the default) links the dependencies' models.
4. A companion is not attempted while its main goal is open.
5. Companions no longer have a lower heartbeat limit of their own.
6. A havoc value has its site's type (`h_hv`), `Grow` is modelled (a
   panic for a negative count, else nothing), and the size bound ends a
   run like fuel.

A rerun on these three libraries is reported separately.
