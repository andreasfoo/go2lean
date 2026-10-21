# go2lean

Find bugs in Go functions by translating them into an executable semantic
model in Lean 4, searching that model for counterexamples, and verifying every
counterexample with the real Go toolchain. An LLM agent proposes the
properties; Lean finds the inputs; `go test` is the judge.

```
Go source ─► go/ssa ─► Lean SSA model ─► counterexample ─► go test ─► verdict + PoC
            M1 programmatic        M2 constraint engines / agent        M3 poc
```

Status: working proof of concept. Four ground-truth packages run end to end
in 2–5 s each; every planted bug is confirmed by a real Go run and every
correct function gets a bounded certificate or "no counterexample". The design
and roadmap are in [docs/design.md](docs/design.md); the Go semantics
modelled (and how faithfully) are listed in [docs/semantics.md](docs/semantics.md).

## Quick start

Requirements: Go 1.26 and [elan](https://github.com/leanprover/elan). The Lean
toolchain pinned in `lean/lean-toolchain` is fetched on the first build.

```sh
go run ./cmd/go2lean run ./examples/overflow
```

```
== M1 compile
compiled github.com/andreasfoo/go2lean/examples/overflow -> out/lean/Go2Lean/Generated/gossa/overflow.lean
  SpecSum      complete  func(xs []int, ret int) bool  [spec for Sum]
  Sum          complete  func(xs []int) int
== M2 properties
  p_overflow_sum_nopanic       no_panic       Sum does not panic on any input
  p_overflow_sum_specsum       postcondition  Sum satisfies the spec-in-Go oracle SpecSum
== M2 hunt
  p_overflow_sum_nopanic       random: none in 2000 trials; exhaustive: holds on 9724 inputs (recorded as exhausted)
  p_overflow_sum_specsum       random: found at trial 12 -> cx_overflow_sum_specsum_random_s42
      inputs map[xs_len:5 ...] memory map[xs:[1 0 0 0 9223372036854775807]] model ok -9223372036854775808
== M3 poc
  cx_overflow_sum_specsum_random_s42  confirmed    real: map[ret:-9223372036854775808]
      PoC: out/poc/cx_overflow_sum_specsum_random_s42
```

The PoC directory holds a ready-to-run Go test and a README with the
reproduction command. Everything the modules exchange is a JSON record under
`out/` (types in `internal/contract`).

## How it works

**M1 — programmatic.** `golang.org/x/tools/go/ssa` lowers the package; the
frontend turns it into a frontend-independent IR (blocks with parameters,
one dialect op per instruction) and prints it as a Lean term over the `GoSSA`
dialect. The Lean side is a small SSA core in the spirit of
[lean-mlir](https://github.com/opencompl/lean-mlir): a dialect-parametric
program representation with a fuel-bounded, total interpreter. Slice contents
come from a read-only memory oracle fixed per run; values the frontend cannot
model are explicit havoc inputs. The model may over-approximate; it never
silently guesses. Lean core only — no Mathlib.

**M2 — constraint engines and agent.** Two property shapes are templated:
`no_panic` for every modelled function, and `postcondition` for every
**spec-in-Go** function — a Go function with a `// go2lean:spec Target` doc
comment and signature `func Spec(<Target params>..., ret <result>) bool`,
compiled into the model with the same pipeline as the target so the Lean
property and the Go oracle agree by construction. Two engines run per
property: seeded random search (per-trial value profiles, plus the target's
own integer literals so magic-constant branches are reachable), then
exhaustive enumeration of a small domain (narrow signatures only), which
records an `exhausted` certificate when it passes (`exhausted_havoc` when
some inputs ran through values the model does not know, of which it tried
only a few). A spec whose verdict
depended on a havoc value (it looked inside an opaque value, or called
something unmodelled) is reported as unknown, never as a counterexample
or a certificate. Both run in `go2lean-search`, a native
executable built by lake from the Lean engines and every generated model,
driven by a JSON description of the search; properties are searched in
parallel, and each engine stops at a wall-clock budget (10 s by default; 120 s
under `agent`)
so one function with expensive inputs cannot stall a sweep. go2lean keeps its own memory under a
third of physical memory (a soft limit; `GOMEMLIMIT` overrides it), and
`GO2LEAN_MEMPROFILE=<file>` writes a heap profile near a run's peak. The agent sits on top: it reads the source and
the model report, writes specs into `<pkg>/go2lean_specs_test.go`, repairs
the ones the frontend rejects, runs the pipeline, and writes a report from
the verdicts.

**M3 — poc.** A counterexample becomes a Go test in the target package,
which executes the real function with the generated inputs: run go2lean on
a scratch clone, since a function that writes files will write them. The
test never fails by itself; it prints what happened and the `poc` package
judges: `confirmed` (the real run violates the oracle the way the property
says), `spurious` (it does not — with a diagnosis of what the model got
wrong), or `inconclusive`. This is the only source of trust: a bug is
reported only with a verdict from here.

## The agent

```sh
export ANTHROPIC_API_KEY=...        # or `ant auth login`
go run ./cmd/go2lean agent ./examples/oob
go run ./cmd/go2lean agent -llm mock:internal/agent/testdata/oob ./examples/oob   # offline replay
```

The LLM (Claude Opus 5.5 through the Anthropic SDK by default) is behind a
one-method interface. `-model` and `-base-url` point the same client at any
Anthropic-compatible endpoint (`-thinking=false` if the provider rejects the
parameter); `mock:<dir>` replays scripted responses (`round-1.md`,
`round-2.md`, …) for tests and offline runs.

```sh
go run ./cmd/go2lean agent -model MiniMax-M3 -base-url https://api.minimaxi.com/anthropic ./examples/oob
``` Every prompt and response is
written to `out/agent/<package>/`, and the report lands in `out/agent/<package>/report.md`. A
revised spec file in the report round triggers one more pass.

## Commands

```
go2lean compile [-workspace out] <package>        Go -> Lean model, ABI, targets
go2lean props   [-workspace out] -package <path>  template properties from the model report
go2lean hunt    [-workspace out] [-property id] [-engine random|exhaustive|all] [-jobs n] [-budget s]
                [-seed n] [-trials n] [-fuel n] [-maxlen n] [-exhaust-len n] [-exhaust-limit n]
go2lean poc     [-workspace out] -counterexample id [more ids...]   one go test per package
go2lean run     [-workspace out] [-nil-inputs] <packages...>   compile, props, hunt, poc (./... allowed)
go2lean agent   [-workspace out] [-llm claude|mock:<path>] [-model id] [-base-url url] [-thinking]
                [-repairs n] [-report] <package>
```

## Ground truth

`go2lean run -deps -workspace out-reg ./examples/...` runs every row
(`-deps` models the generic helpers of package `slices` the examples call).
`scripts/regress.sh out-reg` does the same and compares every property's
outcome (confirmed, certified, none) with
[`testdata/regress/examples.golden`](testdata/regress/examples.golden);
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
| `examples/portable` | constructs that gave spurious or missed results on one-api and spf13/cast: `URL` slices the `BaseURL` of a `*meta.Meta` (a struct of exported fields from another package, which has a function returning one); `Pick` indexes with `rand.Intn(len(xs))`; `Parse` and `parse` (ids differ only in case) divide by `strconv.Atoi`'s result; `Port` calls the generic `Must`; `Unit` reads group 2 of a one-group regexp; `Minor` divides by group 2 of a version, read through a helper that takes the regexp; `Head` divides by the first element after checking a sentinel error; `Per` divides by a `uint8` case of a type switch on `any`; `Label` reads a field of a handle (`*meta.Session`), then divides by a name's length; `Tokens` divides through a pointer only `InitEncoder` sets; `ring.push` would panic on a ring with an empty buffer, which only a receiver built field by field has; `Shrink` and `Budget` use `min`/`max`; `Drain` divides by an `atomic.Int64`; `Kind.String` and `Label2` read a stringer's index table | `URL`, `Pick` (Go's own `invalid argument to Intn`), `Parse`, `parse`, `Port` (via `Must[int]`), `Head`, `Per` (with `uint8(0)`), `Unit`, `Minor` (`"v0.0"`, a string the search drew from the pattern), `Label`, `Shrink`, `Drain`, `Label2` and `Tokens` confirmed (`Tokens` with the note `reads_unknown_state`: the program may call `InitEncoder` first, as one-api's `CountToken*` rely on `InitTokenEncoders`); `Count` (`errors.Is` on the sentinel `first` returns covers the empty slice), `Digit` (`rand.Intn` stays below its bound), `IntPart` (a one-group match has two elements), `PerMB` (`var mb = kb * 1024` folded), `PerMax` (`Atoi` of an out-of-range number is `MaxInt64`, not 0) and `Month` (both ends of a regexp group checked) certified (`Fill`, through `newRing`, `Budget` and `Kind.String` too); `ring.push` is no target (its unexported type is built by `newRing` only); the helper `group` as a target of its own is inconclusive (a `*regexp.Regexp` input cannot be generated) |

## On a real module

```sh
go run ./cmd/go2lean compile -workspace out-tingly -module ../tingly-box ./...   # 156 packages, 15 s
go run ./cmd/go2lean agent -workspace out-tingly -module ../tingly-box -model MiniMax-M3 \
    -base-url https://api.minimaxi.com/anthropic github.com/tingly-dev/tingly-box/remote/scenario/builtin/claudecode
```

On tingly-box (7539 functions and methods) the model covers 7112; a full
pass over its 156 packages (7095 properties) takes about three hours at
four jobs and ends with 140 confirmed findings (80 of them not involving an
input the test could only pass as nil), 152 spurious counterexamples and
2779 exhaustive certificates (see the design doc, §9b): `truncate(s, n)`
helpers that panic on a negative `n`, `strings.Repeat` with a negative
count, writes to nil maps, slices of short hashes, and many nil
dereferences of fields a caller is expected to set. The agent on
`internal/typ` wrote 63 specs and found `pickWithinTier` indexing an
empty slice.

The tingly-box case study (hand-written specs, the rounds of hunting,
PoCs, full-pass history and one page per finding) lives in its own
repository, [andreasfoo/go2lean-tingly-box](https://github.com/andreasfoo/go2lean-tingly-box).
Its first finding: a hand-written postcondition on
`ConvertAnthropicBetaToOpenAIRequest` is refuted and confirmed by M3:
tool_use ids of 43 to 51 characters reach the assistant `tool_calls[].id`
untruncated while the tool-result side is truncated to 40 (tingly-box
issue #1924).

## Supported Go subset

Legend: ✅ exact (counterexamples and certificates are sound) · 🟡 havoc or
assumption (may give spurious counterexamples, which `go test` rejects, or
miss behaviours) · ❌ the function is rejected and listed in the model report.
Construct-by-construct detail: [docs/semantics.md](docs/semantics.md).

**Values**
- ✅ Integers of every width (wrapping arithmetic, shifts, conversions, `min`/`max`), `bool`
- ✅ Strings: indexing, slicing, concatenation, `==`, `range` rune by rune, `[]byte` / `[]rune` conversions
- ✅ Structs, pointers, recursive types, arrays, slices (`make`, `new`, `append`, `copy`, 3-index slicing)
- ✅ Maps the program builds (insertion order for `range`: 🟡)
- ✅ Interfaces holding values the program boxed: type assertions, type switches, method calls
- ✅ Closures, function values, method values; methods; multiple results
- ✅ `error` nil-ness; sentinel errors (`errors.New` / `fmt.Errorf` of a constant, `var errX = &T{...}`) with `==`, `errors.Is`, `errors.As`
- 🟡 Floats and complex numbers (opaque; JSON numbers keep their encoding)
- 🟡 Interfaces, maps and funcs the model did not build (results of unmodelled calls)
- ❌ Ordered string comparison (`s < t`)

**Control and effects**
- ✅ Loops, branches, recursion (fuel-bounded), calls within the package, calls into dependencies (`-deps`)
- ✅ Panics: index out of range, slice bounds, nil dereference, division by zero, negative shift, `make` length, type assertion, nil map write, `panic(v)`
- ✅ Package variables with constant initializers, constant lookup tables
- 🟡 Other package variables (havoc), `init` functions (not run), stores to globals (ignored)
- 🟡 `defer` of an unmodelled call (ignored); `recover` (havoc)
- ❌ `defer` of a modelled function, goroutines, `select`, channel operations

**Standard library and SDKs** (✅ unless noted)
- `strings`: `HasPrefix` `HasSuffix` `Contains` `Index*` `LastIndex*` `Count` `Cut` `Trim*` `ReplaceAll` `Split` `Fields` `Join` `Repeat` `Builder` `Replacer` (literal pairs); `TrimSpace` `ToLower` `ToUpper` `EqualFold` ASCII only 🟡
- `strconv`: `Atoi` `Itoa` `ParseInt` `ParseUint` `ParseBool` `ParseFloat` (decimal; inf/NaN/hex 🟡) `FormatInt` `FormatUint`
- `unicode/utf8`, `unicode/utf16`, `net/url`: translated from their Go source
- `encoding/json`: `Marshal`, `Unmarshal`, `Valid`, `Encoder` to a `strings.Builder`; `encoding/base64` standard encodings; `encoding/hex.EncodeToString`
- `regexp` with constant patterns; `fmt.Sprintf` with `%s %d %v %t` 🟡 otherwise; `reflect` `ValueOf` `IsZero` `Kind` `IsNil`
- `sync/atomic` integer types; `sync.Mutex` and friends ignored (single-threaded model)
- anthropic-sdk-go and openai-go JSON unions, `Message.Accumulate` (under `-deps`)
- 🟡 `math/rand` (any value in range), clocks, environment, I/O, network (havoc)

On tingly-box (7539 functions and methods, including agent-written specs)
this models 7112 (2025 completely); most of the 427 rejected are test
helpers taking `*testing.T`, `select` statements and `defer` of modelled
functions.

## C and LLVM IR

The LLVM line runs C (or any LLVM IR) through the same SSA core, with
LLVM's integer and memory semantics taken from
[VeIR](https://github.com/opencompl/veir): integers that can be poison,
byte-addressed objects, and undefined behaviour where LLVM (and, for
signed overflow, C) says so. It needs LLVM's development files and the
sanitizer runtime (`llvm-18-dev`, `libclang-rt-18-dev` on Ubuntu) and a
build with the LLVM tag:

```
go build -tags llvm18 -o go2lean ./cmd/go2lean
./go2lean llvm -workspace out examples/llvm/oob.c
```

```
  sum      random: model index_out_of_range ... -> confirmed ERROR: AddressSanitizer: heap-buffer-overflow
  avg      random: model undefined_behavior ...  -> confirmed runtime error: signed integer overflow: 2147483647 + 7 ...
  max_of   no UB in 2000 trials; exhaustive: no UB on all 40 inputs
```

Each function is searched for inputs with undefined behaviour, assuming a
pointer parameter followed by an integer is a buffer and its length; a
counterexample is replayed as a C program under AddressSanitizer and
UndefinedBehaviorSanitizer. The first build of `lean-llvm/` fetches VeIR and
compiles the part of it the dialect imports (under two minutes); later runs
reuse it. What is modelled so far is in
[docs/design.md §4.4](docs/design.md).

## Layout

```
cmd/go2lean/           CLI
internal/contract/     shared JSON records and the workspace layout
internal/frontend/     M1 frontends: gossa (go/ssa), llvm (go-llvm, -tags llvm18)
internal/ir, ssaout/   program representation, validation, Lean printer
internal/constraint/   M2 core: property templates and engine runners
internal/agent/        M2 agent: LLM interface, Claude, mock, prompts, loop
internal/poc/          M3: harness generation, execution, verdicts
internal/llvmhunt/     the LLVM line: model, search, C replay under ASan/UBSan
internal/pipeline/     the steps the CLI and the agent share
lean/                  Lean 4 project: SSA core, GoSSA dialect, search engines
lean-llvm/             Lean 4 project: LLVM dialect over VeIR, its search driver
<workspace>/lean/      a workspace's own Lean project: its models and search driver
examples/              ground-truth packages
docs/design.md         design and roadmap
docs/semantics.md      the Go semantics modelled, by construct, with status
```

## Roadmap

Completion is counted in items; ✅ done, 🚧 in progress, ⬜ not started.

**Go line (go/ssa → GoSSA)**: 12 / 15
- ✅ SSA core with an executable, fuel-bounded semantics; GoSSA dialect
- ✅ Heap model: pointers, slices, arrays, structs, writes through aliases
- ✅ Maps, interfaces, closures, function values, methods
- ✅ Standard library and SDK models (list above); dependencies from source (`-deps`)
- ✅ Engines: random search, exhaustive bounded certificates
- ✅ M3: every counterexample replayed as a `go test`, verdict with PoC
- ✅ Spec-in-Go properties and probes (model value vs real value)
- ✅ LLM agent: proposes specs, runs them, reports
- ✅ Regression gate in CI (166 properties); benchmark of hand-found bugs (34/34)
- ✅ Differential tests of the JSON, regexp, UTF-16 and number models against Go
- ✅ Proof prototype: `bv_decide` proves a spec for all 64-bit inputs (`lean/Proofs`)
- ✅ Real-world run: tingly-box, 82 bugs found and confirmed ([go2lean-tingly-box](https://github.com/andreasfoo/go2lean-tingly-box))
- 🚧 Proof engines in the pipeline (`bv_omega` for linear functions, `bv_decide` for loop-free ones)
- ⬜ Floats; `init` functions and package state
- ⬜ Goroutines, `select`, channels, `defer` of modelled functions

**LLVM line (C, LLVM IR, TinyGo → VeIR semantics)**: 5 / 10
- ✅ `lean-llvm/`: LLVM dialect over the same core with [VeIR](https://github.com/opencompl/veir)'s integer and memory semantics
- ✅ C frontend (clang + mem2reg + go-llvm); random and exhaustive search; replay under ASan/UBSan
- ✅ TinyGo IR (LLVM 22): Go's ABI, runtime panics, aggregates, function values, globals, callees from the standard library and runtime lowered from their IR
- ✅ `go2lean tinygo -compare`: the same package through both lines, side by side
- ✅ Agreement on the example packages where both lines model the code (design doc §4.4)
- 🚧 Replay of TinyGo-line counterexamples with `go test`
- 🚧 Package `init` functions, inputs for strings and interfaces in the TinyGo line
- ⬜ Specs in C; properties beyond "no undefined behaviour"
- ⬜ Floats, `ptrtoint`/`inttoptr`, more intrinsics, `malloc`/`free`
- ⬜ CI for the LLVM line (needs LLVM development files)

**Agent and scale**: 2 / 4
- ✅ Resumable full passes over a module; parallel searches
- ✅ Specs from an issue tracker's bug classes (tingly-box: 28 confirmed, 11 new bugs)
- ⬜ Ranking targets by expected yield; `ModelGap` feedback into the frontend
- ⬜ Generalising a confirmed bug across a package

## License

MIT, see [LICENSE](LICENSE).
