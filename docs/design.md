# go2lean design

> Status: v0.3 (2026-10-17). This document is the overall design, used to
> agree on goals, boundaries, interfaces and milestones before and while
> building. v0.3 records what the first end-to-end slice actually built and
> what it changed in the design (notably the memory oracle, §4.2).
> Items marked **unverified** are judgement calls that a spike must test.

## 0. One sentence

Translate Go programs into executable SSA semantic models in Lean, let an
agent state properties over those models, use Lean's decision tools to find
**counterexamples**, and turn every counterexample into a **reproducible PoC**
with the real Go toolchain. When no counterexample exists, the same model is
used for **proof**.

## 1. Goals and non-goals

### 1.1 Goals

1. **Three modules forming a closed loop.**
   - M1 `programmatic`: deterministic translation. Two frontends feed one Lean
     SSA core:
     - **F-GoSSA**: Go → `golang.org/x/tools/go/ssa` → Lean `GoSSA` dialect (first).
     - **F-LLVM**: Go → TinyGo → LLVM IR → Lean `LLVM` dialect (later; also the
       entry point for other languages).
   - M2 `agentic`: an LLM agent states properties over the model and drives
     Lean to produce counterexamples or proofs. Its non-agentic core (property
     templates and engines) is the `constraint` package.
   - M3 `poc`: realises counterexamples as real Go tests, runs them with the
     Go toolchain and judges them.
2. **Trust comes from M3, not from the model.** The model may be imprecise;
   every bug reported to a human carries a PoC that reproduces under `go test`.
3. **One core, several dialects.** The Lean SSA core is frontend-independent.
   A dialect defines only types, instructions and the semantics of a single
   instruction; CFG interpretation, fuel, havoc and panics live in the core.
4. **Cross-checking.** The same Go function compiled by both frontends yields
   two models that can be run on the same inputs. A disagreement is either a
   translation bug or a TinyGo/gc semantic difference; both are worth reporting.
5. **Two-sided output.** Every property ends as a `confirmed` PoC, a
   `proved`/`bounded_proved` Lean theorem, or an explicit `unknown` with a reason.

### 1.2 Non-goals (first phase)

- Goroutines, channels, `sync`, data races: not modelled. M3's `-race` is
  extra evidence only.
- GC, finalizers, `unsafe`, cgo, reflection, the fine semantics of
  `defer`/`recover`.
- Floating point: a function using it is marked unmodellable.
- Whole-program verification. The unit is the **function-level** property.

## 2. Design principles

### P1. The model may over-approximate, never under-approximate (for counterexamples)

Anything the translation does not model (unknown external calls, unmodelled
memory operations, runtime functions) becomes **havoc**, an explicit
non-deterministic input. It is never guessed and never aborts the whole function.

- Under over-approximation, **no counterexample ⇒ none in the real program**
  (the proof direction is sound).
- Counterexamples found under over-approximation **may be spurious**; M3
  filters them. M3 is the oracle, so M1 need not be perfect.
- The one permitted under-approximation is loop fuel; such results are
  labelled `bounded_proved(fuel = N)`, never `proved`.

### P2. Havoc is an explicit parameter

Havoc values are extra **inputs** of the model function, not `sorry` or
axioms. `bv_decide`/random search can choose them, counterexamples carry their
values, and when M3 says `spurious` the diagnosis can name the havoc site the
counterexample depended on.

### P3. Every property has two forms: a Lean proposition and an executable Go oracle

Disagreement between the two is the most dangerous failure mode. The
mitigation is **spec-in-Go** (§5.2): the agent writes only the Go form; M1
derives the Lean form mechanically through the same translation pipeline, so
they agree by construction.

### P4. A counterexample is re-validated inside the model before it reaches M3

Whatever Lean output a counterexample is parsed from, it is re-executed in
the model (or reported from the executing run itself) before being written as
a `Counterexample` record.

### P5. Core and dialect are separate

The core knows only typed values, blocks with parameters, terminators, fuel,
havoc and panics. This is lean-mlir's split, restricted to the SSA core; the
rewriting framework and general regions are out of scope.

## 3. System overview

```
                 ┌──────────────────────────────────────────────────┐
                 │ schemas / internal/contract: Target, Property,    │
                 │ Counterexample, Verdict, ProofResult, ModelGap    │
                 └──────────────────────────────────────────────────┘
   Go source
     │
     ├─► F-GoSSA ──► GoSSA dialect terms ─┐
     │                                    ├──► Lean SSA core ──► executable model + abi.json
     └─► F-LLVM  ──► LLVM dialect terms ──┘    (Go2Lean.SSA)          │
                                   ▲                                 ▼
                      crosscheck ◄─┘               ┌──────────────────┐ Counterexample ┌──────────┐
                                                   │ M2 agentic       │ ─────────────► │ M3 poc   │
                                                   │ constraint core  │ ◄───────────── │ go test  │
                                                   └──────────────────┘    Verdict     └──────────┘
                                                            ▲
                      M1 ◄──────── ModelGap ────────────────┘
```

Modules communicate **only through JSON records in a workspace directory**
(§7), so each runs and tests on its own.

## 4. The Lean SSA core (`Go2Lean.SSA`)

### 4.1 Why a core

- Both frontends share CFG interpretation, fuel, havoc, panics and value
  encoding; a frontend emitter only translates instructions.
- Cross-checking is "run two programs through one interpreter".
- Properties are written the same way for both routes.
- Adding Rust/C (via LLVM) or another Go frontend touches no core code.

### 4.2 Structure (as built)

```lean
class Dialect (Op : Type) (Val Oracle : outParam Type) where
  denote : Oracle → Op → List Val → Res Val   -- one instruction; may panic, may read the oracle
  toBool : Val → Option Bool                    -- branch conditions

inductive Instr (Op) | op (dst : Var) (o : Op) (args : List Var) | havoc (dst : Var) (site : Nat)
inductive Term
  | ret (v) | jump (b) (args) | br (cond) (t) (targs) (f) (fargs) | panic (k : PanicKind)
  | call (f : FuncId) (args) (dst) (next) (nargs)

structure Block (Op) where params : List Var; instrs : Array (Instr Op); term : Term
structure Func (Op)  where name; params : List Var; nvals : Nat; entry : BlockId; blocks : Array (Block Op)
structure Prog (Op)  where funcs : Array (Func Op); havocWidths : Array Nat

def run (p : Prog Op) (or : Oracle) (h : Havoc Val) (fid) (args) (fuel : Nat) : Res Val × HState Val
```

- **Phi → block parameters.** Both `go/ssa` `Phi` and LLVM `phi` become block
  parameters; the core has no phi.
- **`denote` is branch-free and call-free.** Single instructions are pure
  functions (they may panic and may read the oracle). All control flow is in
  `Term`. This is what lets `simp` unfold `run` for a fixed fuel into a
  straight-line bitvector expression for `bv_decide`, the same route lean-mlir
  takes with `simp_peephole`.
- **`Res α := ok α | panic PanicKind | outOfFuel`.** `outOfFuel` is a model
  artefact; `panic` is observable behaviour. `PanicKind.modelStuck` marks an
  ill-typed or malformed model (a frontend bug), never a Go panic.
- **Oracle (new in v0.3).** Read-only ambient state fixed for a whole run.
  For GoSSA it is memory: `mem : ref → index → BitVec 64`. Two reads of the
  same location agree, across functions — necessary because a spec-in-Go
  function reads the same slice as the function it specifies. Site-keyed
  `havoc` remains for values with no location (unmodelled calls).
- **Fuel** is consumed per block entry and per call; every recursion is
  structural on fuel, so the interpreter is total and `#eval`-able.

Deep embedding plus a denotation, rather than emitting shallow Lean
functions directly: one more layer of Lean work in exchange for both
frontends sharing everything and translation being testable apart from semantics.

### 4.3 `GoSSA` dialect

Stage A (built): integers of every width, bools, read-only slices of integers.

| go/ssa | GoSSA op / term | Status |
|---|---|---|
| `BinOp` arithmetic / bitwise / comparisons / shifts | `add sub mul sdiv udiv srem urem and or xor andNot shl shr eq ne slt … uge`; shift ≥ width gives 0 or sign fill, negative signed count panics | built |
| `UnOp` `-` `^` `!` | `neg bnot not` | built |
| `Convert` / `ChangeType` between integer types | `conv w signExtend` | built |
| `If` / `Jump` / `Return` / `Panic` | `Term` | built |
| `Phi` | block parameters | built |
| `Call` to a modelled function in the same package | `Term.call` (the block is split at the call) | built |
| `Call` to a builtin `len`/`cap` on a slice | `len` / `cap` | built |
| `Call` to anything else | havoc result, function marked `partial` | built |
| `IndexAddr` then `UnOp *` | `load w` (bounds check + oracle read) | built |
| `Slice` `xs[lo:hi]` | `reslice` with Go bounds panics | built |
| `Store` to a slice element | ignored, function marked `partial` | built (read-only model) |
| `Alloc` / `FieldAddr` / maps / interfaces / closures / multiple results | function rejected | later |
| `Go` / `Send` / `Select` / `Defer` | function rejected | out of scope |

**Go semantics, not LLVM:** `x / 0` and `x % 0` panic; `MinInt / -1` wraps;
negative indices are out of range (indices are compared unsigned after
sign-extension).

**Memory model stages:**
- **A (built, read-only):** a slice is `(ref, off, len, cap)`; element reads go
  to the oracle. Bounds checks only need `len`, so index-out-of-range
  properties need no memory contents. Writes mark the function `partial`.
- **B:** a heap `Ref → Obj` threaded through functions; `Store`, `*`,
  `IndexAddr`, `MakeSlice` become heap operations and aliasing is exact.
- **C:** interfaces, closures, map iteration (as havoc).

### 4.4 `LLVM` dialect (not built yet)

**Build it on lean-mlir.** lean-mlir already defines per-operation semantics
for the LLVM dialect in Lean (integer arithmetic with poison/UB, `icmp`,
`select`, bit manipulation, and recently multi-block control flow). When
F-LLVM is started, the first step is to read that dialect and either port its
`denote` definitions into `Go2Lean.Dialect.LLVM` or depend on it directly;
the only go2lean-specific parts are Go's panic checks and the mapping of
TinyGo runtime calls. Writing LLVM integer semantics from scratch is a
mistake we should not make.

Same core; ops `add … icmp select zext sext trunc`, `llvm.*.with.overflow`,
`br condbr switch ret unreachable`, with `call @runtime.*Panic; unreachable`
mapped to `Term.panic`. `sdiv`/`srem` get Go's panic checks inserted at
translation time (recorded in the model report). Memory starts as havoc.

Pipeline: `tinygo build` IR export (`-o x.ll` or `-printir`, **unverified**) →
`opt -passes=mem2reg,instnamer` → `github.com/llir/llvm` → emitter. The
target must be pinned to a 64-bit native target so `int` matches `go test`;
wasm has a special i64 ABI and is excluded.

Value of this route: other LLVM languages, cross-checking GoSSA, and catching
TinyGo-vs-gc differences. Its memory model is much weaker, so memory
properties stay with GoSSA.

### 4.5 Frontend emitters (Go side)

```
internal/frontend/gossa/   go/packages + go/ssa → ir.Prog            (built)
internal/frontend/llvm/    tinygo + opt + llir  → ir.Prog            (later)
internal/ir/               frontend-independent program representation
internal/ssaout/           Lean printer, abi.json, model report, targets
```

Both emitters produce the same `ir.Prog`; the Lean printer is written once.

### 4.6 ABI bridge

M3 needs to turn model values back into Go values. The GoSSA ABI is nearly
the identity; the LLVM ABI follows TinyGo's expansion rules (slice →
`ptr,len,cap`, string → `ptr,len`, structs flattened, multiple results → an
LLVM struct). Both produce the same record:

```json
{
  "go_func": "github.com/andreasfoo/go2lean/examples/overflow.Sum",
  "go_sig": "func(xs []int) int", "frontend": "gossa",
  "params": [{"go": "xs", "go_type": "[]int", "kind": "slice", "elem": "int",
              "model": ["xs_ref", "xs_off", "xs_len", "xs_cap"]}],
  "result": {"go_type": "int"},
  "havoc": []
}
```

Counterexamples carry slice contents under `memory` (per parameter), read
from the oracle, so M3 materialises exactly the slice the model saw.

### 4.7 M1 outputs

- `lean/Go2Lean/Generated/<frontend>/<pkg>.lean`
- `<workspace>/models/<frontend>/<Func>.json` (ABI), `report.json` (per function:
  complete / partial / rejected, issues, spec-for, Lean FuncId)
- `<workspace>/targets/<id>.json`
- (later) `crosscheck.json`

## 5. M2: properties and counterexamples

### 5.1 Inputs

Go source and type information, the model report, ABI records,
cross-check results, and optionally a natural-language focus from the user.

### 5.2 Property kinds and spec-in-Go

| kind | meaning | Lean form | Go oracle |
|---|---|---|---|
| `no_panic` | no panic under a precondition | `run f args ≠ .panic _` | mechanical: `defer recover()` |
| `postcondition` | the result satisfies a predicate | derived from the spec function | the agent's `Spec(args…, ret) bool` |
| `equivalence` | `f ≡ g` | `run f = run g` | run both, compare |
| `crosscheck` | GoSSA model ≡ LLVM model | core-level comparison | `go test` vs `tinygo test` |
| `custom` | the agent writes Lean directly | agent-written | agent-written (mandatory) |

**Spec-in-Go** (built): the agent — or a human — writes a Go function with a
`// go2lean:spec Target` doc comment, e.g. `func SpecSum(xs []int, ret int)
bool`. M1 compiles it into the model with the same pipeline, and the property
is `run Sum args = .ok r → run SpecSum (args ++ [r]) = .ok true`. The Lean
form and the Go oracle agree by construction; nobody has to write Lean.
`custom` is the only place the agent writes Lean, and its counterexamples are
flagged separately.

### 5.3 Engine ladder

1. **Random search** (built): `Go2Lean.Search.run`, a seeded generator with
   per-trial *value profiles* (boundary-only, small, mixed) so that correlated
   inputs are likely; memory and havoc are re-drawn every trial from the trial
   seed. The value pool includes every integer literal of the target and its
   spec, with off-by-one neighbours, so comparisons against magic constants
   are reachable. Reports one JSON line. Seconds.
2. **Exhaustive small domain** (built): `Go2Lean.Search.exhaust` enumerates
   boundary values, mined literals and magnitudes ≤ 8 for every integer
   parameter and slice element, slices up to length 3, with a domain-size
   cap. No counterexample yields a `ProofResult` of status `exhausted`, a
   weak but honest bounded certificate; havoc sites are not enumerated and
   the record says so.
3. **Proof for linear functions**: `bv_omega` after unfolding `run` for a
   fixed fuel; polynomial, no SAT.
4. **`bv_decide`, conditionally**: only for loop-free functions or fuel ≤ ~8,
   with a hard timeout, after the engines above found nothing. Bit-blasting
   is dominated by `mul`/`div` and by loop unrolling, and the interpreter has
   to be `simp`-unfolded first (the `h ▸` width casts in `binInt` are hostile
   to that). Measure on `Clamp` before investing further.
5. **SMT export** (Z3/cvc5) when complete bug-finding on loops is needed:
   external, asynchronous, better `mul`/`div` heuristics; no Lean certificate,
   which bug-finding does not need because M3 is the certificate.
6. **Proof**: `simp`/`omega`/`decide`, then agent-written Lean.

Counterexamples are re-validated per P4. The random engine reports the values
it actually executed, so revalidation is implicit.

### 5.4 Agent loop (built, first version)

`internal/agent` drives one package:

```
1. compile the package (M1) to learn which functions are modelled
2. ask the LLM for go2lean_specs_test.go: spec-in-Go functions for the modelled
   functions, inside the supported subset (system prompt states the rules)
3. write the file into the package, compile again; specs the frontend rejects
   go back to the LLM with the frontend's issues (up to -repairs rounds)
4. propose (templates: no_panic + one postcondition per spec), hunt, verify (M2/M3)
5. report round: verdicts, counterexamples and proof results go back to the LLM,
   which writes out/agent/report.md and may return a revised spec file; a
   revision triggers one more pass through 3–4
```

Design choices:
- Specs live in a `_test.go` file of the target package, so production code is
  untouched and the frontend sees them by loading the package's internal test
  variant (`go/packages` with `Tests: true`).
- The LLM is behind a one-method Go interface (`Complete(system, user)`).
  `Claude` uses the Anthropic SDK (Claude Opus 5.5, adaptive thinking);
  `Mock` replays scripted responses from a directory, used by tests and for
  offline demonstration.
- Every prompt and response is written to `<workspace>/agent/`, so a run is
  inspectable and replayable without the model.
- A postcondition run where the target panicked before the oracle could be
  evaluated is *moot*: it neither refutes nor supports the property, and the
  exhaustive engine issues no certificate when any input was moot. The
  `no_panic` property carries that finding instead.

Not yet: ranking properties by expected yield, `ModelGap` records from
spurious verdicts, generalising a confirmed bug to neighbouring functions, and
proof attempts.

## 6. M3: counterexample → real verification (built)

### 6.1 Harness generation

Into the target package, M3 writes `zz_go2lean_poc_<id>_test.go`:

```go
func TestGo2LeanPoC_cx_sum_specsum_s42(t *testing.T) {
    xs := append(make([]int, 0, 5), 1, 9223372036854775806, 9223372036854775807, 0)
    var ret int
    res := map[string]any{"panicked": false}
    func() {
        defer func() { if r := recover(); r != nil { res["panicked"] = true; res["panic"] = fmt.Sprint(r) } }()
        ret = Sum(xs)
        res["ret"] = fmt.Sprint(ret)
    }()
    if !res["panicked"].(bool) { res["spec"] = SpecSum(xs, ret) }
    b, _ := json.Marshal(res)
    fmt.Println("GO2LEAN_RESULT " + string(b))
}
```

The test never fails by itself; it prints a result line and the `poc`
package decides. That keeps "the harness broke" separate from "the property
is violated".

### 6.2 Running and judging

`go test -count=1 -v -run '^TestGo2LeanPoC_<id>$' <pkg>` with a fixed timeout
(`tinygo test` for `crosscheck` properties, later).

| status | condition |
|---|---|
| `confirmed` | the real run violates the Go oracle in the way the kind requires (a panic of the expected category for `no_panic`; the spec returning false for `postcondition`) |
| `spurious` | the real run satisfies the oracle |
| `inconclusive` | compile failure, timeout, missing result line, a panic where the oracle could not be evaluated, or a panic category mismatch |

`spurious` carries a diagnosis (model output vs real output, havoc sites used,
whether the property was `custom`); this is the only feedback channel into M1/M2.
`confirmed` produces `<workspace>/poc/<id>/` with the test file, a README with
the reproduction command, and the verdict.

## 7. Shared contract

JSON records; Go types in `internal/contract`. Integers are decimal strings,
rendered per the Go type in the ABI (signed/unsigned). The workspace layout:

```
<workspace>/targets/<id>.json
<workspace>/properties/<id>.json
<workspace>/counterexamples/<id>.json
<workspace>/verdicts/<counterexample id>.json
<workspace>/proofs/<property id>.json
<workspace>/models/<frontend>/<pkg>/{report.json, <Func>.json}
<workspace>/poc/<counterexample id>/
```

```jsonc
// Target
{"id":"t-sum","module":"github.com/andreasfoo/go2lean","package":".../examples/overflow",
 "func":"Sum","go_sig":"func(xs []int) int","models":{"gossa":"models/gossa/Sum.json"}}

// Property
{"id":"p_sum_specsum","target":"t-sum","kind":"postcondition",
 "nl":"Sum satisfies the spec-in-Go oracle SpecSum",
 "spec":{"go_func":"SpecSum","lean_decl":"Go2Lean.Generated.gossa.overflow.f0"},
 "frontend":"gossa","source":"template"}

// Counterexample
{"id":"cx_sum_specsum_s42","property":"p_sum_specsum","frontend":"gossa","engine":"random","fuel":1000,
 "inputs":{"xs_ref":"0","xs_off":"0","xs_len":"4","xs_cap":"5"},
 "memory":{"xs":["1","9223372036854775806","9223372036854775807","0"]},
 "model_output":{"res":"ok","value":"-2"},"revalidated_in_lean":true}

// Verdict
{"counterexample":"cx_sum_specsum_s42","status":"confirmed",
 "evidence":{"cmd":"go test -count=1 -v -run '^TestGo2LeanPoC_cx_sum_specsum_s42$' ...","exit":0,"stdout_tail":"..."},
 "observed":{"ret":"-2"},"poc_dir":"out/poc/cx_sum_specsum_s42"}

// ProofResult
{"property":"p_clamp_specclamp","status":"bounded_proved","fuel":16,"tactic":"bv_decide",
 "lean_file":"lean/Go2Lean/Proofs/p_clamp_specclamp.lean","time_ms":3200}

// ModelGap
{"from_verdict":"cx_0050","frontend":"gossa","kind":"unmodeled-call",
 "site":"f.go:42 call strings.Index","suggestion":"model as length-bounded havoc"}
```

## 8. Repository layout and CLI

```
go2lean/
  cmd/go2lean/              CLI
  internal/contract/        §7 types, workspace IO, validation
  internal/repo/            locating the checkout and the lake project
  internal/ir/              frontend-independent program representation
  internal/frontend/gossa/  F-GoSSA
  internal/frontend/llvm/   F-LLVM (later)
  internal/ssaout/          Lean printer, ABI, model report, targets
  internal/constraint/      M2 core: property templates, engine runners
  internal/agent/           M2 agent loop, LLM interface (later)
  internal/poc/             M3
  lean/                     lake project (Lean 4 core only; no Mathlib, no plausible)
    Go2Lean/SSA/Core.lean        the core
    Go2Lean/Dialect/GoSSA.lean   the GoSSA dialect
    Go2Lean/Search.lean          the random engine
    Go2Lean/Generated/           M1 output (do not edit)
    Go2Lean/Proofs/              M2 output
    Search/                      per-property search files (scratch, ignored)
  examples/                 ground-truth packages
  docs/
```

```
go2lean compile [-workspace out] <package>        M1: Go → Lean model, ABI, targets
go2lean props   [-workspace out]                  M2: template properties from the model report
go2lean hunt    [-workspace out] [-property id]   M2: random search for counterexamples
go2lean poc     [-workspace out] -counterexample id   M3: realise and verify
go2lean run     [-workspace out] <package>        all of the above
```

The Lean side depends on Lean 4 core only (`BitVec`, `bv_decide`, `Lean.Json`);
the random engine is our own so it can print JSON and avoid a dependency on
`plausible`'s output format. No Mathlib: build time and size are not worth it.

## 9. Ground truth and metrics

Each package under `examples/` contains buggy and correct functions, specs,
and expected outcomes.

| package | bug | expected |
|---|---|---|
| `overflow` | `Sum` wraps; `SpecSum` says non-negative inputs give a non-negative sum | `p_sum_specsum` confirmed; `p_sum_nopanic` none |
| `oob` | `Last` has an off-by-one (`i <= len`) | `p_last_nopanic` confirmed (index out of range); `Clamp` none |
| `divzero` | `Avg` divides by a caller-controlled count | `p_avg_nopanic` confirmed (divide by zero); `Ratio` none (`MinInt/-1` wraps, no panic) |
| `magic` | `Decode` indexes an empty payload only when `tag == 0x5EC4E7` | `p_decode_nopanic` confirmed; reachable only because literals are mined into the value pool |

Planned: `shift` (count ≥ 64 / negative count), `lenfield` (untrusted length
driving `make`/slicing), `alias` (writes through aliased slices; GoSSA stage B only).

Metrics: counterexample recall, spurious rate, unknown rate, proof rate,
crosscheck agreement, end-to-end time. All of `examples/` runs on every
change to M1's modelling rules.

## 9b. Smoke test on a real module (2026-10-17)

`tingly-box` (156 packages, 1152 Go files, an LLM gateway) was compiled with
the go/ssa frontend in one pass (15 s). What it taught:

| finding | consequence |
|---|---|
| 3783 top-level functions, **15 complete, 2 partial, 3766 rejected** — all at the signature | stage A's subset is the ceiling, not the engines; bodies were never reached |
| rejections by type kind: pointer 1324, string 897, named struct/interface 727, other slices 272, map 126, `[]string` 104, `context.Context` 91, interface 86, multiple results 51, `error` 35 | order of work: (1) multiple results + opaque `error` (cheap, unlocks the `(T, error)` idiom); (2) strings as `(ref, len)` over the memory oracle; (3) the heap model for pointers, structs, maps — the bulk |
| the module's `replace` target was a git submodule missing from a shallow clone; the tool said only "package has errors" | load errors are now reported with their causes |
| per-package loads re-type-checked the whole dependency graph (minutes per package) | `compile ./...` loads once; 156 packages in 15 s |
| packages sharing a base name (`types`, `util`) overwrote each other's records and Lean modules | workspace records and Lean modules are keyed by the full import path |
| `init#N` package initialisers were modelled as functions | skipped |
| the modelled functions are byte/rune classifiers, `gcd`, `max`, small budget arithmetic — exactly the shape the engines handle | real bugs in such helpers are rare; the value of go2lean on real code depends on the type-coverage work above |
| named integer types (`time.Duration`, `logrus.Level`) passed the frontend but broke the engines (which parsed type names) and the harness (no import) | the ABI carries width, signedness and the type's import path |
| **first real finding**: the agent (MiniMax-M3 through the Anthropic-compatible endpoint) proposed `SpecBudgetDurationNonNegative` for `claudecode.budgetDuration(seconds int) time.Duration`; the random engine found `seconds = MaxInt64` at trial 12; the real run returned `-1s` — `time.Duration(seconds) * time.Second` wraps for `seconds ≥ 9 223 372 037` | the loop works on real code once a function is modelled; the agent's report named the line and the threshold |
| the agent needs `import "time"` for `time.Duration` in spec signatures; the prompt forbade imports | the rule is now "imports only for the types in spec signatures"; package-level variables of the target are explicitly out of the subset |
| the model put one `go2lean:spec` marker at the top of the file, separated by a blank line; go/ssa saw no doc comment and the specs became targets with `no_panic` properties | a function `Spec<X>...` whose parameters are X's parameters plus `ret` of X's result is taken as a spec for X regardless of the marker; the prompt shows the marker placement |
| agent logs of a second package overwrote the first's | one log directory per package under `<workspace>/agent/` |

## 10. Milestones

- **Done — end-to-end PoC slice.** Contract and workspace; Lean SSA core;
  GoSSA stage A; random engine with mined literals and value profiles;
  exhaustive small-domain engine; go/ssa frontend (package test variants);
  property templates (`no_panic`, spec-in-Go `postcondition`); M3 harness,
  runner and verdicts; agent loop (propose → repair → run → report) behind a
  mockable LLM interface; four ground-truth packages confirmed end to end
  (~2–5 s per package).
- **M1b — `bv_decide` engine.** simp lemma set unfolding `run` for fixed fuel,
  counterexample extraction and re-validation, `ProofResult` for bounded proofs.
- **M2a — agent.** Property proposal over the model report, the engine
  ladder, verdict feedback, `ModelGap` records.
- **M1c — F-LLVM stage A + crosscheck.** TinyGo spike (resolve every
  "unverified"), llir parsing, LLVM dialect, cross-check command.
- **M1d — GoSSA heap (stage B).** **M2b — `custom` properties and the proof agent.**
- **Later:** LLVM byte memory, a second LLVM language, interfaces/closures, concurrency.

## 11. Risks

| risk | effect | mitigation |
|---|---|---|
| `simp`-unfolded `run` blows up for `bv_decide` | loop properties limited to random search | small fuel; loop invariants go to the proof agent; keep `denote` branch-free so unfolding stays linear |
| go/ssa semantic details (shifts, conversions, string indexing) | model ≠ Go | ground truth + M3 + cross-check |
| TinyGo IR structure destroyed by optimisation | LLVM route unreadable | low `-opt`, compare pipeline stages; it is the secondary route |
| spec functions outside the subset | property cannot be compiled | the model report flags the spec's status; the agent rewrites it |
| counterexample parsing tied to Lean output formats | breaks on upgrade | our own JSON-printing driver + P4 |
| `custom` properties with inconsistent forms | false positives/negatives | flagged, tried last, every `confirmed` still goes through M3 |
| rejected callee | callers hit `modelStuck` | reported as `inconclusive`; the model report lists the rejection reason |

## 12. Decisions

Decided:
- Dual frontends over one Lean SSA core; `go/ssa` first, LLVM second as
  cross-check and multi-language entry.
- Deep embedding with a denotation, not per-frontend shallow emitters.
- Stage A slices are read-only; the heap model is a later milestone.
- A read-only memory oracle, shared by all functions in a run, instead of
  site-keyed havoc for loads (v0.3).
- No Mathlib, no `plausible`; Lean core plus our own engine.
- All documentation and code comments in English.

## 13. References

- lean-mlir (SSA theory and LLVM dialect semantics in Lean; the source of the core/dialect split): https://github.com/opencompl/lean-mlir
- `golang.org/x/tools/go/ssa`: https://pkg.go.dev/golang.org/x/tools/go/ssa
- TinyGo calling convention (slices, strings, interfaces expanded to scalars): https://tinygo.org/docs/concepts/compiler-internals/calling-convention/
- TinyGo data type layout: https://tinygo.org/docs/concepts/compiler-internals/datatypes/
- TinyGo compiler pipeline: https://tinygo.org/docs/concepts/compiler-internals/pipeline/
- Lean 4.12 `bv_decide` (built-in CaDiCaL, counterexample assignments): https://lean-lang.org/blog/2024-10-3-lean-4120
- llir/llvm (pure-Go LLVM IR parser): https://github.com/llir/llvm
