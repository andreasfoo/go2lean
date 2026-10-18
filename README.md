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
and roadmap are in [docs/design.md](docs/design.md).

## Quick start

Requirements: Go 1.26 and [elan](https://github.com/leanprover/elan). The Lean
toolchain pinned in `lean/lean-toolchain` is fetched on the first build.

```sh
go run ./cmd/go2lean run ./examples/overflow
```

```
== M1 compile
compiled github.com/andreasfoo/go2lean/examples/overflow -> lean/Go2Lean/Generated/gossa/overflow.lean
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
records an `exhausted` certificate when it passes. A spec whose verdict
depended on a havoc value (it looked inside an opaque value, or called
something unmodelled) is reported as unknown, never as a counterexample
or a certificate. Both run in `go2lean-search`, a native
executable built by lake from the Lean engines and every generated model,
driven by a JSON description of the search; properties are searched in
parallel, and each engine stops at a wall-clock budget (10 s by default)
so one function with expensive inputs cannot stall a sweep. The agent sits on top: it reads the source and
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

| package | planted bug | result |
|---|---|---|
| `examples/overflow` | `Sum` wraps on large inputs | `SpecSum` violated; confirmed with `ret = MinInt64` |
| `examples/oob` | `Last` has an off-by-one | confirmed `index out of range`; `Clamp` certified |
| `examples/divzero` | `Avg` divides by a caller-controlled count | confirmed `integer divide by zero`; `Ratio` (incl. `MinInt/-1`) certified |
| `examples/magic` | `Decode` indexes an empty payload only when `tag == 0x5EC4E7` | confirmed — reachable because literals are mined into the value pool |
| `examples/checked` | `Double` returns `(int, error)` and misses overflow for negative inputs | `SpecDoubleHalves` violated; confirmed |
| `examples/alias` | `Deposit(*Account, int)` overshoots its limit; `Withdraw` accepts negative amounts; `Fill([]int, int)` misses the last element | all confirmed; specs observe the mutated struct and slice. Nil receivers and pointer arguments are tried only with `-nil-inputs` |
| `examples/strs` | `Scheme` slices with an unchecked `strings.Index`; `ParsePort` has no upper bound | both confirmed (`"port:2147483648"`) |
| `examples/list` | `Sum(*Node)` skips the head cell of a linked list | confirmed with a materialised `&Node{...}` |
| `examples/recs` | `First`/`Longest`/`Dedup` index slices of structs and strings without a length check | all confirmed with `[]Item{...}` literals; `Total`, `Names` (built with `append`) and `Push` satisfy their specs |
| `examples/opaque` | `(*Cache).Share` divides by a struct field next to a map; `Lookup(ctx, map, keys)` indexes `keys[0]` | both confirmed: the map and the context are opaque to the model and the test passes `context.Background()` and a nil map |
| `examples/runes` | `(*Counter).Bump` indexes a fixed array with `n%4` under a mutex and a deferred unlock; `Head` returns a 3-index slice with `max < len` | both confirmed (`index out of range [-2]`); `Reverse` (`[]rune` round trip) and `Initials` (`range` over a string) are certified |
| `examples/builder` | `Join` builds a string with `strings.Builder` and indexes an empty result at -1 | confirmed; `Parse`'s `fmt.Errorf` paths and `Tag`'s builder growth are certified |

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

Issue-guided hunting is written up in [docs/hunting-tingly-box.md](docs/hunting-tingly-box.md):
a hand-written postcondition on `ConvertAnthropicBetaToOpenAIRequest` is
refuted and the counterexample is confirmed by M3: tool_use ids of 43 to 51
characters reach the assistant `tool_calls[].id` untruncated while the
tool-result side is truncated to 40. Open issue #1924 describes a mismatch of
tool-call ids between the two sides; the PoC is under `docs/poc-issue-1924/`.

## Supported Go subset

Integers of every width, bools, `error` values (nil-ness; `errors.New`
and `fmt.Errorf` are never nil), strings (with the common `strings.*`
functions, `strings.Builder`, `strconv.Atoi/Itoa`, `[]rune` and `[]byte`
conversions and `range` over a string modelled rune by rune),
structs, pointers, recursive types, arrays, slices with reads and writes,
`make`, `new`, 3-index slices, multiple results,
methods, `len`/`cap`, reslicing, `append` and `copy` (including
`append([]byte, string...)`), loops and branches, calls between functions
in the same package. Slice elements can be any modelled type: structs,
strings, pointers, nested slices. Division by zero, out-of-range indexing,
negative shift counts and nil dereferences panic as in Go; `MinInt / -1`
wraps. Unmodelled calls become havoc values (or a recorded zero/opaque
assumption for types the model cannot draw). Interfaces, maps, funcs,
channels, floats and `unsafe.Pointer` are *opaque* values: they can appear
anywhere (parameters, struct fields, results), the model never looks
inside them, map lookups, type assertions, interface comparisons and
float arithmetic are havoc, map updates are ignored, `range` over a map
runs zero times, and M3 passes their zero value (`context.Background()`
for a context). A deferred call the model does not look into (a mutex
unlock, a close) is ignored; a closure is an opaque func value. Goroutines,
`select` and `defer` of a modelled function still reject the function,
with the reason in the model report.
On tingly-box (7539 functions and methods, including agent-written specs)
this models 7112 (2025 completely); most of the 427 rejected are test
helpers taking `*testing.T`, `select` statements and `defer` of modelled
functions.

## Layout

```
cmd/go2lean/           CLI
internal/contract/     shared JSON records and the workspace layout
internal/frontend/     M1 frontends (go/ssa now; LLVM via TinyGo planned)
internal/ir, ssaout/   program representation, validation, Lean printer
internal/constraint/   M2 core: property templates and engine runners
internal/agent/        M2 agent: LLM interface, Claude, mock, prompts, loop
internal/poc/          M3: harness generation, execution, verdicts
internal/pipeline/     the steps the CLI and the agent share
lean/                  Lean 4 project: SSA core, GoSSA dialect, search engines
examples/              ground-truth packages
docs/design.md         design and roadmap
```

## Roadmap

1. GoSSA heap model (stage B): writes through slices, `make`, arrays, structs.
2. LLVM frontend via TinyGo, built on lean-mlir's LLVM dialect semantics, as a
   cross-check and the entry point for other languages.
3. Proof engines for properties the random and exhaustive engines cannot
   settle: `bv_omega` for linear functions, `bv_decide` conditionally.
4. Agent: ranking by expected yield, `ModelGap` feedback into the frontend,
   generalising confirmed bugs across a package.

## License

MIT, see [LICENSE](LICENSE).
