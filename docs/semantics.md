# Go semantics in go2lean

What part of Go the `gossa` frontend and the `GoSSA` Lean dialect model,
and how faithfully. The layout follows how lean-mlir documents a dialect:
the value domain first, then which operations touch state, how failure is
represented, and the operations by group, each with its status. Section 9
maps lean-mlir's concepts to go2lean's.

Snapshot of go2lean `7f9e10b` (2026-10-20). `docs/design.md` §4.3–4.3c
gives the design, §9b–9c the history of each change.

## Status legend

| status | meaning | effect on results |
|---|---|---|
| **exact** | the model computes what Go computes | counterexamples and certificates are sound for it |
| **havoc** | the result is an unconstrained value of the right type, drawn by the engines (over-approximation) | can produce counterexamples Go does not have; M3 (`go test`) rejects them as spurious |
| **assumption** | the model fixes one behaviour among Go's possible ones and logs that it did (under-approximation) | can hide a bug; a spec verdict that read an assumed value is not counted as decided |
| **stuck** | the model has no value to give (`modelStuck`); the trial is moot | lowers coverage, never produces a wrong verdict |
| **rejected** | the function is not modelled (listed in the model report with the reason) | no properties for it; callers see an unmodelled call (havoc) |

## 1. Value domain

A Go value is one `GoSSA.Val`; the model computes on these, never on Go
memory images.

| Go type | `Val` | status |
|---|---|---|
| `int`, `int8`…`int64`, `uint`…`uint64`, `uintptr`, `byte`, `rune` | `.int w (BitVec w)` | exact (bit-precise, every width) |
| `bool` | `.bool` | exact |
| `string` | `.str ref off len`: a window on an immutable byte array in the heap | exact |
| `error` | `.err nonNil` | nil-ness exact; the message is not modelled; two non-nil errors compare equal (havoc-like) |
| pointer | `.ptr ref path` (heap object + field/element path; `ref 0` is nil) | exact |
| struct | `.structv fields` | exact |
| array `[N]T` | `.arr elems` (a value; `N > 64` is opaque in the ABI) | exact |
| slice | `.slice ref off len cap` over a heap `.arr` | exact |
| map | `.boxed 1 p`, `p` → heap `.arr` of `(key, value)` in insertion order; nil is `.opaqueNil` | exact for maps the program built or the generator made; see §5 |
| interface (incl. `any`) | `.boxed tag v` (`tag` = hash of the dynamic type's name), nil is `.opaqueNil` | exact for values the program boxed or the generator made; `.unit` (unknown) for others |
| func | `.boxed 0 (tuple [fid, captured…])`, nil `.opaqueNil` | exact for functions and closures of the program |
| tuple (multiple results) | `.tuple vs` | exact |
| `float32/64`, `complex64/128` | opaque (`.unit`) | havoc: arithmetic, comparisons and conversions |
| `chan T`, `unsafe.Pointer` | opaque | rejected for channel operations (§8) |

Integers are bitvectors of the Go width, not mathematical integers: the
`int` of a 64-bit target is 64 bits, overflow wraps.

## 2. Effects

As lean-mlir splits ops into pure and impure (`EffectKind`), go2lean's
dialect has `denotePure : Op → List Val → Res Val` for ops that only read
their operands and `denote : Oracle → Heap → Op → List Val → Res (Val ×
Heap)` for those that read or write the heap (allocation, loads, stores,
strings, maps, slices). The heap is threaded by the interpreter (memory
SSA, lean-mlir's "pure form" of memory ops); objects are never freed
(Go's GC makes that sound).

## 3. Failure

Go has no undefined behaviour in the fragment modelled; where lean-mlir
yields UB or poison, Go panics. A run ends in one of:

| outcome | Go | kinds |
|---|---|---|
| `ok v` | returned | — |
| `panic k` | runtime panic or `panic(...)` | `index_out_of_range`, `slice_bounds`, `nil_dereference`, `divide_by_zero`, `negative_shift`, `make_slice_len`, `type_assertion`, `nil_map_write`, `explicit` (a `panic` call), `other` |
| `panic modelStuck` | — | the model cannot continue (stuck); not a Go panic, never reported as one |
| `outOfFuel` | — | the step budget ran out (loops, recursion); moot |

`recover` is not modelled: a panic ends the run.

## 4. Operations by group

### 4.1 Integers and booleans

| Go | model | status |
|---|---|---|
| `+ - * & \| ^ &^` | `add sub mul and or xor andNot` | exact, wrapping |
| `/ %` | `sdiv udiv srem urem` | exact; zero divisor panics; `MinInt / -1` wraps |
| `<< >>` | `shl shr` (signed/unsigned count) | exact; count ≥ width gives 0 or the sign fill; negative signed count panics |
| `== != < <= > >=` | `eq ne slt sle … ult ule …` | exact (signedness from the type) |
| unary `- ^ !` | `neg bnot not` | exact |
| integer conversions | `conv w signExtend` | exact |
| constants | `const` (mined into the engines' value pools) | exact |

### 4.2 Strings

| Go | model | status |
|---|---|---|
| literal, `len(s)`, `s[i]`, `s[lo:hi]`, `s + t`, `s == t`, `s != t` | `strConst strLen strIndex strSlice strConcat eq ne` | exact (bounds panics as Go) |
| `s < t` and other orderings | — | rejected |
| `for i, r := range s` | `strNext` (UTF-8 decoding, invalid bytes → U+FFFD) | exact |
| `for i := range n` (integer) | an ordinary loop | exact |
| `[]byte(s)`, `string(b)`, `[]rune(s)`, `string(rs)`, `string(r)` | `strToBytes bytesToStr strToRunes runesToStr runeToStr` | exact |
| `s[lo:hi:max]` | — | rejected (Go forbids it too) |

### 4.3 Pointers, structs, arrays, slices

| Go | model | status |
|---|---|---|
| `new(T)`, `&T{…}`, escaping locals | `alloc zero` | exact |
| `*p`, `*p = v`, `&x.f`, `&xs[i]`, `x.f`, `a[i]` | `loadPath storePath fieldAddr indexAddr field arrIndex` | exact; nil dereference panics |
| `xs[i]`, `xs[lo:hi]`, `xs[lo:hi:max]` | `indexCheck load reslice reslice3` | exact, bounds panics |
| `make([]T, n, c)` | `makeSlice` | exact; negative/too large length panics |
| `append` | `appendSlice` | exact while capacity allows; growth doubles the capacity (Go's growth is implementation-defined) |
| `copy` | `copySlice` | exact |
| `len`, `cap` of slices, arrays, pointers to arrays | `len cap` | exact |
| `==` on arrays and structs | `eq` / `eqDeep` (strings compared by bytes) | exact for comparable fields; floats inside are havoc |
| struct/array literals | `mkStruct`, `.arr` | exact |

### 4.4 Maps

| Go | model | status |
|---|---|---|
| `make(map…)`, `m[k] = v`, `m[k]`, `v, ok := m[k]`, `delete`, `len` | `makeMap mapUpdate mapLookup mapDelete mapLen` | exact on known maps; nil map write panics, nil read gives zero |
| `for k, v := range m` | `mapRange mapNext` | assumption: insertion order (Go randomises; order-dependent bugs can be missed) |
| a map the model knows nothing about (result of an unmodelled call) | — | lookups havoc, updates ignored (assumption), `range` runs zero times (assumption), `len` havoc below 16 (assumption) |
| keys | string, integer, bool, and comparable values | exact (`keyEq`) |

### 4.5 Interfaces and type switches

| Go | model | status |
|---|---|---|
| storing a value in an interface | `box tag` | exact |
| `x.(T)` for a concrete `T`, comma-ok and panicking | `unbox` | exact on boxed values; havoc on unknown ones |
| `x.(I)` for an interface `I` | `assertIface` (the boxed types implementing `I`) | exact on boxed values |
| method call on an interface value | `dispatch` over the implementing boxed types (at most 64), then `callv` | exact; a nil interface panics; an unknown value takes the call's fallback (havoc) |
| `reflect.ValueOf(x)`, `.IsZero()` | `reflectOf reflectIsZero` | exact on boxed values; other `reflect` calls havoc |

### 4.6 Functions and calls

| Go | model | status |
|---|---|---|
| static call in the package | `call` | exact |
| call into another package compiled together (`-deps`) | `xcall` (the driver links the models) | exact |
| closures, function values, method values | `funcRef`/`xfuncRef` + `callv` (captured values first) | exact; nil func call panics; an unknown func takes the fallback |
| generic functions | instances of package `slices` under `-deps`; other generic code is called unmodelled | partial |
| call to an unmodelled function with a result | `havocInto`: scalars havoc; structs havoc field by field; pointers to a fresh struct of havoc fields; slices of 0–3 zero elements (assumption); interfaces/maps/funcs unknown | havoc / assumption |
| call to an unmodelled function without a result | ignored | assumption (its effects are lost) |
| a modelled callee that gets stuck | the call's fallback binds a havoc result | havoc |
| recursion, loops | fuel-bounded | exact up to fuel (`outOfFuel` beyond) |
| `defer f()` of an unmodelled function | ignored | assumption |
| `defer` of a modelled function | — | rejected |
| `panic(v)` | `panic explicit` | exact |
| `recover()` | havoc | unreachable in practice (a panic ends the run) |

### 4.7 Package-level variables

| Go | model | status |
|---|---|---|
| a variable nothing writes after init, initialised to a constant or a function | its value | exact (writes from `…ForTest` helpers ignored) |
| a zero-valued variable no function writes | the zero value | exact |
| any other load | havoc | havoc |
| stores to globals | ignored | assumption |
| `init` functions | not run | assumption |

### 4.8 Conversions

| Go | model | status |
|---|---|---|
| integer ↔ integer | `conv` | exact |
| named ↔ underlying type, `json.RawMessage` ↔ `[]byte` | no-op | exact |
| string ↔ bytes / runes | §4.2 | exact |
| integer ↔ float, float ↔ float | havoc | havoc |
| others | — | rejected |

## 5. Standard library and SDK models

Calls not listed here are unmodelled (§4.6).

| function | status |
|---|---|
| `strings.HasPrefix HasSuffix Contains Index IndexByte LastIndex Count TrimPrefix TrimSuffix Trim TrimLeft TrimRight ReplaceAll Split Fields Join Repeat` | exact |
| `strings.TrimSpace ToLower ToUpper EqualFold` | exact for ASCII; Unicode spaces and case folding are not modelled |
| `strings.Builder` (`WriteString WriteByte WriteRune String Len Reset`) | exact (a string cell) |
| `strconv.Atoi` | exact for decimal `int64` (sign, range error) |
| `strconv.Itoa` | exact |
| `fmt.Sprintf` with `%s %d %v %t %%` on strings, integers and bools, no flags or widths | exact; any other format or argument: havoc string |
| `fmt.Errorf`, `errors.New` | a non-nil error (message not modelled) |
| `encoding/json.Unmarshal` into `*string`, `*bool`, `*interface{}`, `*map[string]interface{}`, `*[]interface{}` | exact (Go's rules for null, type errors, merging into a non-nil map; numbers are opaque floats; invalid UTF-8 → U+FFFD) |
| `encoding/json.Unmarshal` into structs, `json.Marshal` | havoc (in progress) |
| `encoding/hex.EncodeToString` | exact |
| `reflect.ValueOf`, `(reflect.Value).IsZero` | §4.5 |
| `github.com/google/uuid.New` | a fresh value distinct from every other in the run (real UUIDs collide with negligible probability) |
| `time.Now` and other clocks, `os.Getenv`, I/O, network | havoc |

## 6. Concurrency

| Go | status |
|---|---|
| `go f()`, `select`, channel send/receive, `close` | rejected |
| `make(chan T)` | opaque value (the function is still rejected if it uses the channel) |
| `sync.Mutex` and other `sync` types | opaque values; `Lock`/`Unlock` calls ignored (no concurrency is modelled, so a single-goroutine run is exact) |

## 7. Inputs (what the engines generate)

The search engines build each parameter from its Go type; the M3 harness
writes the same value as a Go literal.

| Go type | generated |
|---|---|
| integers | boundary values, small values, literals mined from the call tree (± 1), per-trial profiles |
| bool, error | both values / nil and non-nil |
| string | the literal pool and its neighbours, digits, random bytes; named string types and fields documented `// Any of "a", "b".` (Stainless SDKs) draw from their constants; length up to `-maxlen` or `go2lean:maxlen N` |
| slices, arrays | up to `maxlen` elements, halved per nesting level |
| structs | field by field; an SDK union (`Of<Variant>` fields) gets one variant |
| pointers | non-nil by default (nil with `-nil-inputs`); recursive types through a bounded type table |
| maps | nil (with `-nil-inputs`) or up to `maxlen` entries, keys mostly from the strings the call tree uses as map keys |
| `interface{}` | nil, string, bool, int, `map[string]interface{}`, `[]interface{}` |
| interfaces with methods, funcs, channels, floats | their zero value (nil, 0) |
| `context.Context` | `context.Background()` |
| unexported fields of another package's struct | their zero value (a test cannot set them) |
| SDK response types' raw JSON (`RawJSON()`) | an object of one or two members keyed by strings the call tree compares with `==`; the harness builds the value by decoding a JSON document |
| pointers to another package's struct that has a constructor (a handle) | opaque non-nil |
| receiver of a method whose type has `NewT` | built by `NewT` (`T.M~NewT`) |

## 8. Not modelled (rejected functions)

Goroutines, `select` and channel operations (so `range` over a channel);
`defer` of a modelled function; string orderings (`<` on strings);
`unsafe` conversions; parameters of type `*testing.T` (test helpers);
generic functions as targets (their instances are not targets either).

Not rejected but worth knowing: `range` over an integer (Go 1.22) is an
ordinary loop in go/ssa and exact; `range` over a function iterator (Go
1.23) is lowered by go/ssa to closures and modelled as such (not yet
covered by an example).

## 9. Correspondence with lean-mlir

| lean-mlir | go2lean | note |
|---|---|---|
| `Ctxt`/SSA core with regions, generic over dialects | `Go2Lean.SSA` core: blocks with parameters, terminators `jump br ret call xcall callv`, generic over `Dialect` | no regions: Go's control flow is flattened by go/ssa |
| dialect = op set + `denote` | `GoSSA` dialect: `Op`, `denotePure`, `denote` | an `LLVM` dialect is designed (§4.4 of the design) but not built |
| types as bitvectors of width `w` | `Val.int w (BitVec w)` | Go's fixed widths, no arbitrary-width integers |
| `EffectKind` pure / impure | `denotePure` / `denote` (heap) | the split is by function, not yet a per-op tag |
| memory: block id → block, pointer `(block, offset)` | heap: `ref` → object, pointer `(ref, path)` | Go has no pointer arithmetic, so a typed path replaces a byte offset |
| pure memory ops (memory SSA) | the heap threaded through `denote` | the explicit-`mem` form is the representation to switch to for proofs |
| UB / `PoisonOr` | Go panics (`Res.panic k`) and `modelStuck` | Go defines the behaviour the C/LLVM world leaves undefined |
| refinement proofs between programs | properties of one program: `no_panic` and spec-in-Go postconditions; counterexamples checked by `go test`, small-domain certificates, `bv_decide` proofs (§5.3) | go2lean finds bugs rather than verifying rewrites |
| `simp`/`bv_decide` over unfolded programs | random and exhaustive engines; proofs where the domain is small or the model is loop-free | |
