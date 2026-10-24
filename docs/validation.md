# Validation

[← Back to README](../README.md)

[Scope and evidence](#scope-and-evidence) · [Ground truth](#ground-truth)

## Scope and evidence

What it is: a bounded, search-based bug finder (random search steered by the program's
constants, exhaustive search of small domains), not a symbolic executor;
the pipeline proves over all inputs `no_panic` of integer and bool
functions, and the postconditions `go2lean prove` is given: loop-free
functions alone, loops with invariants an agent writes
(`examples/invariants`) or by hand (`lean/Proofs/Functional.lean`: up to
insertion sort, sorted and a permutation, for every slice). 29 ground-truth packages run in CI (251 properties, each outcome
pinned, including every spec's mutation score). The design is in
[docs/design.md](../docs/design.md); the Go semantics modelled, and how
faithfully, in [docs/semantics.md](../docs/semantics.md); what it does not
deliver, in [docs/limitations.md](../docs/limitations.md). There are no
releases yet: the CLI, the workspace formats and the model's semantics
still change from one pull request to the next.

Evidence beyond the codebase it was built on comes from blind runs with a
protocol committed before each run and the tool frozen:
[Masterminds/semver](../docs/eval/semver.md) (templates alone found no bug;
the agent's blind specs found one, half new, and Go's own fuzzer on the
same specs found one more; most counterexamples were spurious from one
model gap, fixed since and reported separately) and
[gobwas/glob](../docs/eval/glob.md) (6 new bugs and 1 known, all from the
agent's specs or its reading of the source; the most serious one, a wrong
match, was found by reading and reached by no search; templates again
found none) and [concurrent code](../docs/eval/conc.md) (eapache/go-resiliency
and patrickmn/go-cache: every concurrency result confirmed was real, the
one bug, a data race in a package's own test, new, and found as well by
`go test -race`; the run found two frontend bugs, fixed since). The 85 tingly-box findings
were triaged by hand ([go2lean-tingly-box](https://github.com/andreasfoo/go2lean-tingly-box)).

## Ground truth

`go2lean run -deps -workspace out-reg ./examples/...` runs every row
(`-deps` models the generic helpers of package `slices` the examples call).
`scripts/regress.sh out-reg` does the same and compares every property's
outcome (confirmed, certified, none) with
[`testdata/regress/examples.golden`](../testdata/regress/examples.golden);
`scripts/json-diff.sh out-jd` checks the `encoding/json` model against Go,
`PKG=testdata/rediff scripts/json-diff.sh out-rd` the `regexp` model, and
`PKG=testdata/convdiff` the `strconv` model. All run on every push.

| package | planted bug | result |
|---|---|---|
| `examples/overflow` | `Sum` wraps on large inputs | `SpecSum` violated; confirmed with `ret = MinInt64` |
| `examples/oob` | `Last` has an off-by-one | confirmed `index out of range`; `Clamp` certified |
| `examples/divzero` | `Avg` divides by a caller-controlled count | confirmed `integer divide by zero`; `Ratio` (incl. `MinInt/-1`) certified |
| `examples/magic` | `Decode` indexes an empty payload only when `tag == 0x5EC4E7` | confirmed — reachable because literals are mined into the value pool |
| `examples/checked` | `Double` returns `(int, error)` and misses overflow for negative inputs | `SpecDoubleHalves` violated; confirmed |
| `examples/alias` | `Deposit(*Account, int)` overshoots its limit; `Withdraw` accepts negative amounts; `Fill([]int, int)` misses the last element | all confirmed; specs observe the mutated struct and slice. Nil receivers and pointer arguments are tried only with `-nil-inputs` |
| `examples/strs` | `Scheme` slices with an unchecked `strings.Index`; `ParsePort` has no upper bound | both confirmed (`"port:2147483648"`); `Short` slices a `fmt.Sprintf("%s-%d", ...)` label to 8 bytes: confirmed (`slice bounds out of range [:8] with length 6`), and a spec on `Label` (the formatted string) certified |
| `examples/list` | `Sum(*Node)` skips the head cell of a linked list | confirmed with a materialised `&Node{...}` |
| `examples/recs` | `First`/`Longest`/`Dedup` index slices of structs and strings without a length check | all confirmed with `[]Item{...}` literals; `Total`, `Names` (built with `append`) and `Push` satisfy their specs |
| `examples/opaque` | `(*Cache).Share` divides by a struct field next to a map; `Lookup(ctx, map, keys)` indexes `keys[0]` | both confirmed: the map and the context are opaque to the model and the test passes `context.Background()` and a nil map |
| `examples/runes` | `(*Counter).Bump` indexes a fixed array with `n%4` under a mutex and a deferred unlock; `Head` returns a 3-index slice with `max < len` | both confirmed (`index out of range [-2]`); `Reverse` (`[]rune` round trip) and `Initials` (`range` over a string) are certified |
| `examples/builder` | `Join` builds a string with `strings.Builder` and indexes an empty result at -1 | confirmed; `Parse`'s `fmt.Errorf` paths and `Tag`'s builder growth are certified |
| `examples/xpkg` | `Share` guards negative totals, then calls `money.Pct` in another package, which divides by a zero total | confirmed through the linked model of both packages; `Bounded` (via `money.Clamp`) certified; `Units` divides by an unexported field of `money.Amount` plus one, which no test outside `money` can set: certified (the field is its zero), while `Amount.PerScale~NewAmount` inside `money` is confirmed (`NewAmount` accepts a scale of -1); `Post` takes a `*money.Ledger`, a handle `money.NewLedger` builds: certified (a ledger assembled field by field, with a nil map, is not an input) |
| `examples/iface` | `Mixed` puts a string into a `[]any` queue whose `Sum` asserts every item to `int`; `Unwrap` asserts a nil `any` | all confirmed as `interface conversion` (`Sum` also on a queue holding nil items); `Count` (comma-ok assertions), a spec on `Push` and a spec on `Pipe.Keep` with a nil filter hook certified; `Ratio` divides by the area of a `Shape` built as a square of side `n` (a method call on an interface): confirmed; specs on `Area` (value and pointer receivers through an interface), `Name` (an assertion to an interface and a call through it, like `param.IsOmitted`) and `Set` (`reflect.ValueOf(v).IsZero()`) certified |
| `examples/maps` | `Pick` indexes `names[pos[want]-1]` with the position of a missing or first name; `Counter.Share` divides by a limit `NewCounter` accepts as 0 | both confirmed (`index out of range [-1]`; `integer divide by zero` through `Counter.Share~NewCounter`, the receiver built by the constructor); `Registry.Put` is targeted as `Registry.Put~NewRegistry` and certified (a registry no constructor made has a nil map: a misuse, not a target); `Distinct` (`len` of a built map) and `Sum` (`delete`, `range`) certified |
| `examples/globals` | `At` indexes a package-level slice nothing fills; `Slot` indexes four slots with a generator the package initializer sets to one that yields 7 (only a `...ForTest` helper replaces it) | both confirmed (`index out of range`): a package-level variable nothing writes loads as its zero value, one only the initializer sets to a constant or function loads as that; `Blank` (`==` on `[4]byte` against such a variable) certified |
| `examples/closures` | `Last` returns the last element a closure appended, and indexes `-1` when it never ran | confirmed (`index out of range [-1]`); `HasNeg` (a function passed as a value), `HasAbove` (a closure through `slices.ContainsFunc`) and `Total` (a closure updating a captured variable) certified; `anyOf` with a nil func confirmed as `nil_opaque_input` |
| `examples/stream` | a converter that assembles content-block events into one message drops the open block's text when a second `Start` arrives; properties of the event sequence, written as `go2lean:sequence` specs | `SeqComplete` (every byte received is in the message at the end of the stream) confirmed on a 7-event sequence found by random search; `SeqBounded` (an invariant after every event) holds on all 538,084 sequences of up to 3 events |
| `examples/conc` | goroutines, channels and locks: `Notify` sends on an unbuffered channel nobody reads when `n > 10`; `First` returns before reading a second result; `AddMore` locks a held mutex; `Drain` closes a channel twice; `ReadWhileWriting` takes a read lock while a writer waits (writer preference); `Bump` locks a package-level mutex twice; `WithDeadline` and `Timeout` leave a sender blocked when the deadline or the `time.After` timer wins; `LostWakeup` waits on a `sync.Cond` once, after the `Signal` was sent; `RacyCount`, `CountHits` (a package variable), `FillAndPeek` (a map read without the lock), `SnapshotRacy` (an atomic add against a plain copy) and `AddInside` (`WaitGroup.Add` inside the goroutine it counts) race, and `PeekEarly` reads a result before waiting for it | `Notify`, `AddMore`, `Bump`, `LostWakeup` (deadlock), `First`, `WithDeadline`, `Timeout` (goroutine leak) and `Drain` (`close of closed channel`) confirmed by `go test`; the six races confirmed by `go test -race`; `ReadWhileWriting` found by the model and confirmed, by Go's scheduler or, when it does not take the interleaving, by replaying the model's schedule; `Pipe`, `Sum`, `TrySend` (`select` with `default`), `Once`, `SumGen` (`range` over a channel `gen` returns), `CountAtomic`, `BumpAll`, `Cancelled` (a worker that watches `ctx.Done()`), `TimeoutBuffered`, `Poll` (a ticker), `Handoff` (`Cond.Wait` in a loop), `LockedCount`, `AtomicCount`, `Publish` and `FillAndRead` (race-free), `SnapshotAtomic`, `AddBefore`, `LazyClose` (a channel made on first use, closed after a nil check) and the rest hold on every input, most under every schedule (C3) |
| `examples/portable` | constructs that gave spurious or missed results on one-api and spf13/cast: `URL` slices the `BaseURL` of a `*meta.Meta` (a struct of exported fields from another package, which has a function returning one); `Pick` indexes with `rand.Intn(len(xs))`; `Parse` and `parse` (ids differ only in case) divide by `strconv.Atoi`'s result; `Port` calls the generic `Must`; `Unit` reads group 2 of a one-group regexp; `Minor` divides by group 2 of a version, read through a helper that takes the regexp; `Head` divides by the first element after checking a sentinel error; `Per` divides by a `uint8` case of a type switch on `any`; `Label` reads a field of a handle (`*meta.Session`), then divides by a name's length; `Tokens` divides through a pointer only `InitEncoder` sets; `ring.push` would panic on a ring with an empty buffer, which only a receiver built field by field has; `Shrink` and `Budget` use `min`/`max`; `Drain` divides by an `atomic.Int64`; `Kind.String` and `Label2` read a stringer's index table | `URL`, `Pick` (Go's own `invalid argument to Intn`), `Parse`, `parse`, `Port` (via `Must[int]`), `Head`, `Per` (with `uint8(0)`), `Unit`, `Minor` (`"v0.0"`, a string the search drew from the pattern), `Label`, `Shrink`, `Drain`, `Label2` and `Tokens` confirmed (`Tokens` with the note `reads_unknown_state`: the program may call `InitEncoder` first, as one-api's `CountToken*` rely on `InitTokenEncoders`); `Count` (`errors.Is` on the sentinel `first` returns covers the empty slice), `Digit` (`rand.Intn` stays below its bound), `IntPart` (a one-group match has two elements), `PerMB` (`var mb = kb * 1024` folded), `PerMax` (`Atoi` of an out-of-range number is `MaxInt64`, not 0) and `Month` (both ends of a regexp group checked) certified (`Fill`, through `newRing`, `Budget` and `Kind.String` too); `ring.push` is no target (its unexported type is built by `newRing` only); the helper `group` as a target of its own is inconclusive (a `*regexp.Regexp` input cannot be generated) |
