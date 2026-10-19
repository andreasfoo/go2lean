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
Stage B (built, §4.3b): heap-backed slices of any modelled element type,
structs, pointers, strings, `append`/`copy`.

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

### 4.3b Stage B: the heap model (built, first version)

The smoke test (§9b) fixed the order: pointers and structs first, strings
second. As built: `Dialect.denote` takes and returns the heap (`Array Val`,
slot 0 reserved for nil), so the interpreter core only threads it; the
dialect gains `ptr (ref, path)`, `structv`, `arr` values and the ops
`alloc`, `loadPath`, `storePath`, `fieldAddr`, `indexAddr`, `field`,
`mkStruct`, `makeSlice`, `appendSlice`, `copySlice`. The generators materialise every slice argument and
every pointee on the heap before the run, so stores are exact and aliasing
is real; the oracle remains only for slices that come out of unmodelled
calls. A spec runs on the post-run heap, so it observes mutations through
its arguments (`examples/alias`: a deposit that overshoots its limit, a fill
that misses the last element, nil dereferences — all confirmed by `go test`).
Counterexamples carry struct and pointer arguments under `objects`; the
harness materialises `&T{F: v, ...}` and `func() *int { v := ...; return &v }()`.
Slice elements are any modelled type (`examples/recs`: `[]Item`, `[]string`,
built with `append`); `append` writes in place while the capacity allows and
otherwise copies into a fresh backing array of twice the capacity (Go's
exact growth is implementation-defined and visible only through `cap`).
Interfaces, maps, funcs, channels, floats and `unsafe.Pointer` are opaque
values (§9b); `range` over strings is modelled, over maps assumed empty;
deferred calls the model does not look into are ignored. Not yet:
closures, goroutines, `select`, `defer` of modelled functions.

The original design notes, kept for the parts still to do:

- **Heap.** `Heap := Ref → Obj` with `Obj := .array (elems : Array Val) |
  .struct (fields : Array Val) | .string (bytes : Array (BitVec 8))`. The
  heap is part of the run state next to `HState`, not of the oracle: the
  oracle keeps supplying contents of *inputs* (slices, strings and pointed-to
  values the caller owns), the heap holds what the function allocates or
  writes. A read goes to the heap when the `Ref` was allocated or written
  during the run, else to the oracle. This keeps stage-A models valid and
  makes writes exact for aliasing: two slices with the same `Ref` see each
  other's stores.
- **Values.** `Val` gains `.ptr (ref : Nat) (path : List Nat)` (a pointer to
  a field/element path inside an object; `nil` is `ref = 0`) and
  `.structv (fields : List Val)` for struct values. Pointer comparison is
  `ref`+`path` equality; `nil` dereference panics `nilDereference`.
- **Ops.** `alloc (kind)`, `load/store` generalised to a path, `field i`,
  `fieldAddr i`, `indexAddr`, `makeSlice`, `append` (grow-and-copy with a
  fresh `Ref` when `cap` is exceeded, as Go does), `copy`. Because `denote`
  stays branch-free and call-free, these are single steps; the loop in
  `copy`/`append` is a bounded fold over `len`, which is acceptable for
  the random and exhaustive engines and is where `bv_decide` would need a
  summary lemma instead.
- **Frontend.** `Alloc` → `alloc`; `FieldAddr`/`IndexAddr` → address values
  (paths), `Store` → `store`; struct parameters are flattened by the ABI
  into their fields (as TinyGo does for LLVM, so the two frontends agree on
  the ABI); pointer parameters are generated as either `nil` or a fresh
  `Ref` whose pointee comes from the oracle.
- **M3.** The harness materialises structs from the flattened fields and
  pointers as `&T{...}` or `nil`; a counterexample's `memory` section grows
  per-`Ref` object dumps.
- **Strings (after the heap).** `string` is `(ref, len)` over immutable
  bytes; `len`, indexing, slicing and `==` are ops (equality is a bounded
  byte fold); `strings.Contains/HasPrefix/Index/TrimSpace`, `bytes.*` and
  `strconv.Atoi/Itoa` get hand-written models in a `Go2Lean/Dialect/GoSSA/
  Stdlib.lean` table keyed by import path, because a havoc result for
  every `strings` call makes a string function's model meaningless.

Acceptance for stage B: `examples/alias` (writes through aliased slices),
a struct-returning constructor with an invariant spec, and a re-sweep of
tingly-box with the pointer/struct rejection counts as the metric.

### 4.3c What lean-mlir does for memory (read 2026-10-17, `SSA/Projects/SLLVM`)

lean-mlir's *structured LLVM* dialect is the closest prior art and confirms
the shape of our heap model while pointing at two things to borrow:

- **Memory is a map from block ids to blocks**, a pointer is `(block id,
  offset)`, a block holds its contents as one wide bitvector plus a
  live/freed flag; loads and stores are range-checked against the block and
  raise UB otherwise. That is our `ref` + path model at byte granularity.
  Go has no pointer arithmetic, so the typed path form is right for GoSSA;
  the byte form is what the LLVM dialect (§4.4) should port directly,
  including `PoisonOr` for UB where we use `Res.panic`/`modelStuck`.
- **Every memory op exists in two forms**: an *impure* one that runs in an
  effect monad (`load : ptr → m (bitvec w)`), and a *pure* one that takes
  and returns the memory as an explicit SSA value (`loadPure : (mem, ptr) →
  (mem, bitvec w)`, "memory SSA"). Pure programs stay in the fragment that
  `simp`/`bv_decide` handle. Our `Dialect.denote : Oracle → Heap → Op → args →
  Res (Val × Heap)` is exactly the pure form, threaded by the interpreter
  rather than appearing as a variable; when the proof engines arrive, an
  explicit `mem` value in the generated program is the representation to
  switch to, and lean-mlir shows it costs nothing in the dialect.
- `EffectKind` (`pure`/`impure`) tags each op so the framework knows which
  ops touch state; our `denotePure`/`denote` split is the same idea and
  should become an `Op.effect` function so the unfolding lemmas can skip
  heap threading for pure ops.
- Recursive data types are not a dialect concern in lean-mlir either: they
  are pointers into blocks. The type-directed generation of bounded linked
  structures (§4.3b, the ABI type table) is go2lean-specific because our
  inputs are Go values, not memory images.
- Their model has no deallocation and models a single function body; ours
  allocates across calls and never frees, which Go's GC makes sound.

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
   seed. The value pool includes every integer and string literal of the
   target and its spec, with off-by-one and numeric-suffix neighbours, so
   comparisons against magic constants are reachable. Inputs are materialised
   on the heap (slices, strings, structs, pointees, bounded-depth recursive
   structures from the ABI type table). Top-level pointers and receivers are
   non-nil unless `-nil-inputs` is given. A run on which the model got stuck
   (ill-typed, or a call into a rejected function's stub) is *moot*: it never
   counts as a refutation. Reports one JSON line. Seconds.
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
   **Measured (prototype, `lean/Proofs/OobClampIdentity.lean`):** for all
   64-bit `v lo hi`, the model of `oob.Clamp` followed by
   `SpecClampIdentity` returns true; checked in 9 s, axioms `propext`,
   `Classical.choice`, `Quot.sound` only. What made it work: the needed
   functions are looked up in the generated program by `rfl` lemmas
   (`prog.funcs[0]? = some f0`) instead of unfolding all of it (that times
   out), and each op used gets an `rfl` lemma at its width
   (`denote or h .slt [.int 64 a, .int 64 b] = .ok (.bool (a.slt b), h)`)
   instead of unfolding `denote` (generating its equation lemmas times
   out). `simp` then leaves a case analysis on the comparisons, closed by
   `cases`, `simp_all` and `bv_decide`. Not yet working: a spec that calls
   the target again (`SpecClampIdempotent`): after splitting, lookups in
   the environment array (`setIfInBounds` chains) are not reduced, so
   `bv_decide` sees opaque booleans; this needs `getElem!`/`setIfInBounds`
   simp lemmas and a generic split-simp loop before M2 can emit such
   proofs for every loop-free integer function.
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

M3 executes the target with generated inputs inside the target package:
a function that writes files writes them. Run on a scratch clone.

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

All counterexamples of a package go into one test file and one
`go test -count=1 -v -run '^TestGo2LeanPoC_' <pkg>` run (the package's
tests compile once); results are mapped back by test name, and a batch
that does not compile falls back to one run per test. Each PoC directory
still holds a standalone file. (`tinygo test` for `crosscheck` properties,
later.)

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
| after adding multiple results and opaque `error` values, the sweep models 19 real functions (was 15); `(T, error)` alone does not unlock much because the same functions also take strings, pointers or structs | type coverage has to come as a package: the heap model (§4.3b) with structs and pointers, then strings with a modelled core of `strings`/`bytes`/`strconv` |
| heap model (structs, pointers, slice writes) built; `examples/alias` confirmed end to end | 65 complete + 9 partial |
| methods of named types enumerated (the denominator becomes 7498 functions) | 89 complete + 11 partial |
| strings as heap byte arrays with a hand-written `strings`/`strconv` core and literal-driven generation | 464 complete + 35 partial |
| recursive named structs through an ABI type table with depth-budgeted generation (the earlier nil cut-off was wrong: Lean models linked structures fine; only the generator needed a budget) | `examples/list` confirmed |
| unmodelled results of string type havoc'd, other unsupported results take their zero/opaque value as a recorded assumption; arrays; opaque interfaces and maps (covers the `...any` varargs behind every `fmt.Sprintf`) | **468 complete + 389 partial = 857 of 7498 (11.4%)**; remaining signature blockers are interfaces, maps, funcs and `context.Context` |
| first full pipeline over the 111 packages with a model (855 properties): 53 confirmed, 146 spurious, 30 inconclusive | 43 of the confirmed were nil receivers/arguments; 112 of the spurious were stuck models reported as panics; the inconclusive were harness compile errors (name clashes, unused imports, internal/unexported types) |
| after: stuck is moot, nil inputs opt-in, harness fixes, `strings.Split`/`Fields`/`Join`/`Repeat`/`Trim*`/`ToLower`/`ToUpper`/`ReplaceAll`/`IndexByte`/`LastIndex`/`Count`/`EqualFold` modelled, unmodelled pointer results point at fresh zero objects, one `go test` per package | **11 confirmed, 26 spurious, 0 inconclusive**. Confirmed: seven `truncate(s, n)` helpers that slice with a negative `n`, two `strings.Repeat` calls with a negative count, `blobPath` slicing a 3-byte hash to 4, and `budgetDuration` wrapping to a negative duration. The remaining spurious cases are unmodelled calls (see the attribution in the run log) |
| `len`/`cap` on slices of anything but integers was havoc'd (random lengths, spurious index panics); shift counts ≥ 64 made Lean compute `2^k` | fixed; rerunning the 16 packages that had spurious verdicts: **9 confirmed, 5 spurious, 0 inconclusive**; the five that remain depend on `crypto/rand`, `sha256`, `base64`, an OAuth registry and `fmt` — genuine over-approximation that M3 filters. Three more real findings surfaced (`shortenPath`, `sliceLines`, `detectShellOperator`, all slicing with an unchecked length) |
| the largest signature blocker after that was slices of non-integer elements (484 rejections), and `append` rejected every function building a result | slice elements are now any modelled type (`Gen.sliceOf`, `elem_param` in the ABI, `[]T{...}` literals in the harness) and `append`/`copy` are dialect ops; `examples/recs` confirms three planted bugs. **1191 of 7498** |
| 4950 of the remaining rejections were named types: receivers and parameters pointing at structs with a map, interface, func, mutex or channel field, plus `context.Context`, `any` and `*testing.T` | *opaque values*: interfaces, maps, funcs and channels are representable as a value the model never looks into (`Gen.opaque`, `.unit`); reads through them (map lookup, type assertion, interface comparison, calls) are havoc, map updates are ignored, and M3 passes the zero value (`context.Background()` for contexts). Functions taking a `testing` type are test helpers and skipped. A zero array of structs is printed as `List.replicate`, and generated modules set `maxRecDepth` for blocks with hundreds of instructions. **3423 of 7498 (45.7%): 1402 complete, 2021 partial** |
| the engines ran under `#eval` in a fresh `lean` process per property: the interpreter interpreting the interpreter, 50–80 s per property on tingly-box functions with large inputs | `go2lean-search`: a `lean_exe` linking the engines and every generated model (a registry M1 regenerates), driven by a JSON search description (`lean/Go2Lean/Driver.lean`); the same property takes 9 s, the ground-truth packages 10 s each including compile, and properties run in parallel (`-jobs`, default: CPUs). A `*Config` receiver with hundreds of fields still took 27 s per property, so each engine has a wall-clock budget (`-budget`, default 10 s) and reports how far it got |
| floats (1659 struct fields), `unsafe.Pointer`, fixed arrays, `defer mu.Unlock()`, `range` over strings and maps, `json.RawMessage <-> []byte`, 3-index slices | floats and `unsafe.Pointer` are opaque (arithmetic and comparisons havoc); arrays are values (`Gen.arrOf`, `[N]T{...}` in M3; longer than 64 opaque in the ABI); deferred calls the model does not look into are ignored, `defer` of a modelled function still rejects; `range` over a string is a heap iterator advanced by `strNext` (UTF-8 decoded as Go does), over a map runs zero times as a recorded assumption; name-only conversions alias; `[]rune` conversions; `reslice3`; `Index` on strings (new go/ssa) fixed; foreign structs with no exported field (`sync.Mutex`) are opaque parameters; `examples/runes`. **6198 of 7498 (82.7%): 1873 complete, 4325 partial**; 304 of the 1300 rejected are `*testing.T` helpers, the rest closures, `select`, `defer` of modelled functions and results of foreign struct types |
| first pass on the enlarged model (38 packages, 917 properties): 22 confirmed, 5 spurious, 31 inconclusive. Most confirmed were nil dereferences of pointer *fields* the generator left nil; the inconclusive were the harness refusing `internal/` types (a wrong reading of Go's rule: the test lives inside the module) and `[]float64{nil}` | nested pointers non-nil by default (recursive links stay nullable), Go's internal rule applied with the target's package, opaque zero literals, unexported fields of foreign structs opaque. The agent on `internal/routing` (MiniMax-M3, thinking off: with thinking on it spent its whole 32k output budget thinking): 37 specs, all modelled |
| in partial functions most trials were *moot*: `HState.take` (a havoc read) rebuilt the state without `s with`, so every havoc instruction reset the heap and the next load got stuck | fixed; the interpreter now records the op and operand shapes where the model got stuck, the random engine tallies them and `hunt` prints the most frequent site, so the next model gaps come from the engines rather than from guessing |
| with the heap fix, `internal/routing` (37 agent specs): the "recursive links may be nil" rule had made `gin.Context.Request` nullable (five "Request is nil" dereferences); `SpecNewAffinityStage` compared opaque interfaces, so its verdict was a coin flip (four spurious); the exhaustive engine spent minutes building the inputs of a receiver with hundreds of leaves; every remaining moot run was a call into a function rejected for `MakeClosure` | the Go side tells a back-edge (`link`, nullable at every depth) from any other pointer (`ptr`, non-nil by default) along the ABI path; a spec verdict that read havoc is `specUnknown` (moot, never a refutation or a certificate); the exhaustive engine only enumerates signatures with at most 24 leaf generators; closures are opaque func values. The rerun: one confirmed (a nil interface field, the class M3 cannot populate), one spurious, no inconclusive |
| a workspace's `models/` reached 1.7 GB: every occurrence of a nested named struct (gin.Context inside http.Request inside ...) was expanded in place, and a full compile next to a second one ran the machine out of disk and then of memory | every named struct is described once in the ABI's type table and referred to by name (`type_ref`), as recursive types already were; `ResolveSessionID`'s record went from 324 KB to 54 KB. One full compile at a time: go/ssa over all 156 packages with their test variants takes several GB |
| the agent loop on `internal/typ` (MiniMax-M3): 63 specs, one repair round, a report round that revised the file and ran again | two confirmed: `TierTactic.pickWithinTier` indexes an empty slice (defensive: every caller guards it, and the report says so) and a postcondition on `RecordingMode.Has` that ignored `ParseRecordingMode`'s normalisation (the spec was wrong, not the code: the report hedges between the two readings). The loop works end to end on a real package; judging which confirmed spec violations are code bugs is still the reader's job |
| **full pass on the fixed core** (156 packages, 7076 properties, about two hours at four jobs): **97 confirmed, 114 spurious, 167 inconclusive, 2754 exhaustive certificates**. Confirmed by panic: 45 nil dereferences (mostly server handlers reading an interface field the test cannot populate), 18 index out of range, 11 slice bounds, 4 writes to a nil map, 4 negative `strings.Repeat` counts, two explicit panics. The inconclusive were harness compile errors in five classes (imports of types reached through the type table, `_` and `json` as parameter names, variadic parameters, func results under vet, unexported element types), all fixed after the pass; the spurious are over-approximation through havoc values (nil pointers, explicit panics and slice bounds that the real run with those inputs does not reach) | the next pass validates the harness fixes. Coverage at this point: **7112 of 7539 functions (2025 complete, 5087 partial)**, the extra functions being the agent's spec files; models/ is 378 MB |
| the unmodelled callees of the full pass, by call site: `fmt.Errorf` 1533, `fmt.Sprintf` 843, `strings.Builder` methods 454, `fmt.Println/Printf` 615, `json.Unmarshal/Marshal` 497, `time.Now` 230, `errors.New` 83; every spurious counterexample of the pass had used a havoc value | `errors.New` and `fmt.Errorf` are non-nil errors (the message is not modelled); `strings.Builder` is a string cell whose methods are dialect ops, empty as a parameter (a copied non-empty Builder panics in Go); `examples/builder`. The printing and JSON functions stay havoc: their results are rarely branched on |
| the agent on `internal/guardrails/core` (48 specs): one real finding (`detectShellOperator`, already known), one "confirmed" spec violation that was a wrong spec (the report's suggested fix is the current code), and eight spurious postconditions whose *target* had read havoc; every spurious counterexample of the full pass had read havoc too | both engines prefer a refutation whose run read no havoc value and report a havoc-dependent one, so marked, only when nothing better turns up in the budget. The report round then revised the wrong specs and the second pass kept only the real finding. Judging a confirmed spec violation still needs a reader: the agent's own report can argue for a fix that is already there |
| the pass after the first harness fixes (7111 properties): 112 confirmed, 120 spurious, 150 inconclusive, of which 133 were one class: an opaque parameter of a func or map type naming types of other packages (`func(http.RoundTripper) http.RoundTripper`) had no import path in the ABI; the rest were type aliases described inline with their unexported fields, and a parameter named like an imported package | the ABI records every import path a spelling mentions and sees through aliases; the harness renames parameters that shadow an import and tells a compile error from a target that ended the process (`log.Fatal`) |
| **third full pass** (7088 properties, engines preferring havoc-free counterexamples, three hours): **140 confirmed, 126 spurious, 55 inconclusive, 2797 exhaustive certificates**. Confirmed by panic: 56 nil dereferences, 30 index out of range, 18 slice bounds, 18 negative `strings.Repeat` counts, 6 writes to a nil map, a division by zero, an `os.Exit` during the test, and a handful of explicit panics; 50 of the inconclusive were harnesses importing a package by the last element of its path (`anthropic-sdk-go` is package `anthropic`), 4 targets ended the process with `log.Fatal` | the preference raised confirmed findings from 112 to 140 with the same spurious count: the engine keeps looking within its budget for a refutation the real run can reproduce. Imports are now written by package name; the next pass validates |
| issue-guided hunting on tingly-box (see `docs/hunting-tingly-box.md`): a hand-written postcondition on `ConvertAnthropicBetaToOpenAIRequest` is refuted and **confirmed by M3**: tool_use ids of 43 to 51 characters reach the assistant `tool_calls[].id` untruncated while the tool-result side is truncated to 40 (open issue #1924 describes a tool-call id mismatch between the two sides); three helper properties are certified. On the way: SDK unions generated one variant at a time, literals mined transitively, empty slices at depth zero |
| **fourth full pass** (7088 properties, go2lean `0375b2e`): **149 confirmed, 141 spurious, 31 inconclusive, 2794 certificates**; 69 of the confirmed carry `nil_opaque_input`; 26 of the inconclusive are tests a `log.Fatal` in an earlier test of the same batch left without a result (fixed after the pass: each such test reruns alone), 4 harness compile errors (two packages named `anthropic`, fixed: aliases) | 80 confirmed findings do not involve a nil opaque input; by kind: 26 index out of range, 14 slice bounds, 13 nil dereferences inside `protocoltest`, 5 writes to nil maps, 2 negative `strings.Repeat`, 3 interface conversions on nil, an `os.Exit` in `Daemonize`, two spec violations |
| **fifth full pass** (7095 properties, go2lean `d2118de`: unions one variant at a time, transitive literal pool, per-test reruns, import aliases): **140 confirmed (60 with `nil_opaque_input`), 152 spurious, 41 inconclusive, 2779 certificates**. Inconclusive: 26 harnesses redeclared a test because a run killed earlier had left its harness file in the package (fixed after the pass: the directory is cleared first), 13 targets ended the test process (`log.Fatal` in `internal/client` helpers), 2 single oddities | by panic kind: 72 nil dereferences, 38 index out of range, 12 slice bounds, 5 writes to nil maps, 3 interface conversions on nil, 2 negative `strings.Repeat`, 2 spec violations. The pass ran three hours; the container restarted minutes after it finished |
| next histogram: `range` over a map whose unused components have the invalid type (86), zero-value constants of struct type written as nil constants (60), function values (30), `[]T == nil` (16), `make(chan)` (20) | all handled (function values and channels are opaque; slices compare equal to nil exactly when nil; the error interface stays precise after a slip that briefly havoc'd `err != nil`). **6588 of 7498 (87.9%): 1988 complete, 4600 partial**; the 910 left are 319 `*testing.T` helpers, `select`, closures, `defer` of modelled functions |
| hand specs beyond issue #1924 found the model too narrow: `usage.FromOpenAIChatCompletion` returns `ai.NewTokenUsageFull(...)` (another package: havoc), the stream converter's `processEvent` appends chunks through `emitChunk` (a call without a result: ignored) to a `[]interface{}` queue (boxing: opaque), and one stuck callee made a whole run moot | **calls across packages**: a call into a sibling package of the same compile is an `xcall` naming the callee's model; the search driver links every model a search reaches into one program (FuncIds and havoc sites renumbered), an unlinked `xcall` is an unmodelled call. **Calls without a result** to modelled functions are followed. **Every call has a fallback block**: a stuck callee gives a havoc result on the pre-call heap instead of a moot run. **Interface values the program builds** are boxed with a tag for the dynamic type (a hash of the type name); an assertion to a concrete type unboxes it, a mismatch gives `(zero, false)` or the panic kind `type_assertion` (M3: "interface conversion"); **Nil-ness of func, map, channel and interface values**: nil literals, zero values and opaque inputs M3 passes as nil are `opaqueNil`; closures, named functions, `make(map)`, `make(chan)` and boxed values are non-nil; only results of unmodelled calls are unknown. `x == nil` is decided on known values, a type assertion on a nil interface panics, and interface-to-interface conversions keep their value. `examples/xpkg` and `examples/iface`; regression: 24 of 24 counterexamples confirmed, 40 certificates |
| the stream/non-stream and round-trip specs were refuted spuriously; probes showed where: the Anthropic -> Responses stream converter keeps its open blocks in maps (opaque, `range` ran zero times) | **probes** (`go2leanProbe(label, value)` in a spec: the model's values in the counterexample, the real run's in the verdict). **Maps the program builds** are modelled: `make(map)` is a heap list of key/value pairs behind `boxed 1`; lookup (comma-ok), update, `delete`, `len`, `range` in insertion order; keys compare by value, strings by bytes. A nil map reads as zero and panics on write (`nil_map_write`, M3: "assignment to entry in nil map"); maps the model knows nothing about keep the old havoc behaviour, chosen per value at run time. A havoc string read by an oracle makes its verdict unknown (havoc strings are always ""). `run -resume`; `agent -spec-file`. `examples/maps`; regression: 26 of 26 confirmed, 45 certificates |

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
| rejected callee | callers hit `modelStuck` | the call's fallback block havocs the result and the caller goes on; the model report lists the rejection reason |

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
