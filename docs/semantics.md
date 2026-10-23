# Go semantics in go2lean

What part of Go the `gossa` frontend and the `GoSSA` Lean dialect model,
and how faithfully. The layout follows how lean-mlir documents a dialect:
the value domain first, then which operations touch state, how failure is
represented, and the operations by group, each with its status. Section 9
maps lean-mlir's concepts to go2lean's.

Snapshot of go2lean `7f9e10b` (2026-10-20). `docs/design.md` §4.3–4.3b
gives the design, [history.md](history.md) §9b–9c the history of each change.

## Status legend

| status | meaning | effect on results |
|---|---|---|
| **exact** | the model computes what Go computes | counterexamples and certificates are sound for it |
| **havoc** | the result is an unconstrained value of the right type, drawn by the engines (over-approximation) | can produce counterexamples Go does not have; M3 (`go test`) rejects them as spurious |
| **assumption** | the model fixes one behaviour among Go's possible ones and records it in the run (`assume`; under-approximation) | can hide a bug; a spec verdict that read an assumed value is not counted as decided, and an exhaustive pass through one is `exhausted_assuming` with the list |
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
| `min`, `max` on integers | a comparison and `ite` per argument | exact (on strings and floats: havoc) |
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

Whether a map (or an interface or func value) is known is decided at run
time, per value. The model report lists the unknown case as an issue only
where it can happen: not for values known by construction (made or boxed
in the function, inputs, which the search generates concretely, and what
the function reads out of those without writing anything unknown into
memory).

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
| generic functions | modelled per instance (`Must[int]`, concrete types); the generic function itself is not in the model; instances of package `slices` under `-deps` | partial |
| call into a logging package (logrus, log, slog, zap) | results opaque, calls without results ignored | exact for the code under test |
| `apijson.UnmarshalRoot` (the SDKs' `UnmarshalJSON`, unions' `As*` methods) | `jsonDecode` with the target type's description | as JSON decoding above |
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
| an array its initializer fills with constants and nothing else writes, read by element (`var table = [256]encoding{...}`, a stringer's `_X_index`) | the constant array; `table[i]` indexes it (out of range panics, as in Go) | exact |
| a variable initialised to a constant expression over constants and such variables (`var mb = kb * 1024`) | its value, folded at compile time | exact (an expression that would wrap or divide by zero is not folded: havoc) |
| a zero-valued variable no function writes | the zero value | exact |
| any other load | havoc (a confirmed panic whose run read one carries the verdict note `reads_unknown_state`, naming it: the program may initialise it first) | havoc |
| stores to globals | ignored | assumption |
| `init` functions | not run as code; a store an `init` function makes on every path through it (its block dominates every return), to a variable no initializer sets and nothing else writes, counts as that variable's initializer (a constant, a function, a constant `regexp.MustCompile`, a table of constants or functions) | exact for those stores; any other effect of an `init` function: assumption |

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
| `sync/atomic` `Bool Int32 Int64 Uint32 Uint64`: `Load Store Add` | exact: plain reads and writes of the wrapped field (the model is single-threaded); other methods (`Swap`, `CompareAndSwap`): havoc |
| `strconv.ParseInt ParseUint` | exact: bases 0 (prefixes `0b 0o 0x 0`, underscores) and 2–36, bit sizes 0–64, syntax, range (the bound, as soon as the value overflows), base and bit size errors; checked against Go by `PKG=testdata/convdiff scripts/json-diff.sh` |
| `strconv.ParseBool FormatInt FormatUint` | exact (an illegal base panics, as in Go) |
| `math/rand` and `math/rand/v2` `Intn Int31n Int63n IntN Int32N Int64N UintN Uint32N Uint64N` (functions and `*Rand` methods) | any value in `[0, n)` (havoc); Go's panic for `n <= 0` |
| `regexp.MustCompile` of a constant pattern (a constant expression, or `fmt.Sprintf` of a constant format and constant string, integer or bool arguments, folded at compile time); `MatchString FindString FindStringIndex FindStringSubmatch` on it, `regexp.MatchString` with a constant pattern | exact: the frontend compiles the pattern with `regexp/syntax` (Perl syntax, simplified, as `regexp.Compile` does) and the model runs Go's backtracking algorithm on that program (leftmost-first, each (instruction, position) tried once, so linear; empty-width assertions, case folding, UTF-8); `MustCompile` of a valid pattern does not panic. A regexp passed around (a helper's parameter) is one of the package's constant patterns, picked at run time (at most 12; one compiled from a pattern built at run time: havoc). Strings each pattern matches (its shortest match, and one taking every optional part once) join the search's string pool. Checked against Go by `PKG=testdata/rediff scripts/json-diff.sh`. Other methods (`ReplaceAllString`, `FindAll*`, `Split`): unmodelled |
| `fmt.Sprintf` with `%s %d %v %t %%` on strings, integers and bools, no flags or widths | exact; any other format or argument: havoc string |
| `fmt.Errorf`, `errors.New` | a non-nil error (message not modelled) |
| sentinel errors: a package variable initialised (or set by an `init` function on every path) to `errors.New` of a constant or `fmt.Errorf` of a constant format and constant arguments without `%w` (`io.EOF`, `context.Canceled`, `ErrNotFound`), assumed not reassigned | a value of its own (`*errors.errorString` boxed with the variable's identity); `err == S`, `err != S` and `errors.Is(err, S)` exact when `err` is nil or a sentinel (for `==` also a value of an error type), havoc when it is an error the standard library made or the model knows nothing about (it may be, or wrap, `S`) |
| `encoding/json.Unmarshal` into `*string`, `*bool`, `*interface{}`, `*map[string]interface{}`, `*[]interface{}` | exact (Go's rules for null, type errors, merging into a non-nil map; numbers are opaque floats; invalid UTF-8 → U+FFFD) |
| JSON numbers decoded into `float64` or an interface | the bytes `json.Marshal` writes for them when the model knows them: a number of at most 15 significant digits with 1e-6 <= abs < 1e21 (Go then writes it in plain decimal, without trailing zeros), and 0 when the document has no `-0`; any other float is unknown, and encoding it gets stuck. Checked against Go by `PKG=testdata/numdiff scripts/json-diff.sh` |
| `encoding/json.Valid` | exact (the JSON parser the decoding model uses; checked against Go by `PKG=testdata/jsonvalid scripts/json-diff.sh`, 60 same, 0 different over a corpus of edge cases and raw bytes) |
| `encoding/json.Marshal` | exact for strings (Go's HTML-safe escaping, invalid UTF-8 → `\ufffd`), bools, integers, `[]byte` (base64), `json.RawMessage` (compacted and HTML-escaped, as Go does to a Marshaler's output), slices, arrays, pointers, maps with string or integer keys (sorted), interfaces holding a type the package boxes, structs (json tags, `omitempty`, `omitzero` by the type's zero value, `-`, embedded and `,inline` fields); SDK param objects (`default:` constants, `param.Opt`: an omitted Opt is zero, also when its value is a float the model does not know), SDK param unions (one present variant by `param.IsOmitted`; none → null, several → error); floats, `omitzero` on a value of unknown zero-ness, SDK metadata that is set (`SetExtraFields`, overrides, typed `ExtraFields`), `,string` fields, non-empty interfaces, `MarshalText` / `json.Number` and other custom marshalers: stuck. Unexported embedded structs promote their exported fields; `\b` and `\f` are escaped as such (Go 1.22) |
| `encoding/json.Unmarshal` into structs and the types above | exact for the same types (exact key then case-insensitive match, unknown keys ignored, type errors reported while decoding goes on, maps merged, nil pointers allocated); `json.RawMessage` and all raw text are the input's own bytes (a byte-level scan gives each value's source); integers take Go's literal syntax only (`1e2`, `1.0` are errors); duplicate keys: stuck. SDK types decode by the SDKs' apijson rules: exact keys only, null leaves a member unchanged (an Opt becomes null), pointers and maps decoded afresh, metadata with raw text, per-member `respjson.Field` (null or valid) and `ExtraFields` (invalid unless null, as apijson has no decoder for them); a member that does not decode (apijson would coerce or mark it invalid), an SDK struct from a non-object, and union variants decoded from the whole value, members no field reads in an SDK type with metadata or typed `ExtraFields`: stuck |
| `encoding/hex.EncodeToString` | exact |
| `reflect.ValueOf`, `(reflect.Value).IsZero` | §4.5 |
| `(reflect.Value).Kind`, `IsNil` | exact for the types modelled code boxes and the ones the search and the JSON model put in an `any` (the frontend lists each tag's kind); `IsNil` panics for kinds that cannot be nil, as Go does; any other type: stuck. Checked by `PKG=testdata/miscdiff` |
| `unicode/utf16.Encode` | exact (surrogate pairs; invalid runes and surrogates become U+FFFD); checked by `PKG=testdata/miscdiff` |
| `strings.Cut`, `strings.LastIndexByte` | exact |
| `strings.NewReplacer` with literal pairs, `Replace` | exact (first matching old string, left to right) |
| `strconv.ParseFloat(s, 64)` | exact for decimal syntax away from the float64 limit (value as JSON encodes it, syntax and range errors); infinities, NaN and hex floats havoc; checked by `scripts/json-diff.sh` |
| `json.NewEncoder(&b)` on a `strings.Builder`, `SetEscapeHTML` with a constant before every `Encode`, `Encode` | exact (`Marshal`, without HTML escaping when the encoder says so, and a newline); checked by `scripts/json-diff.sh` |
| `unicode/utf8`, `unicode/utf16` | translated from their Go source under `-deps` (as `net/url`) |
| pointer sentinels: a package variable its initializer sets to `&T{...}` of constants (sjson's `errNoChange`) | boxed as an error it compares by identity like `errors.New` sentinels; its fields read as the constants |
| `net/url` | translated from its Go source under `-deps` (the standard-library packages listed in `modelledStdlib`), like a dependency: `url.Parse`, `Hostname`, `Port`, `Query` and the rest are the library's own code. Not modelled inside it: IPv6 literals (`netip.ParseAddr`) and the `urlstrictcolons` GODEBUG setting (a host with two colons), both havoc. Checked by `DEPS=1 PKG=testdata/urldiff` (90 same, 0 different) |
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
| slices, arrays | up to `maxlen` elements, halved per nesting level but at least one; a byte slice (`[]byte`, `json.RawMessage`) is a pool string a third of the time |
| structs | field by field; an SDK union (`Of<Variant>` fields) gets one variant, three times in four one the call tree reads; the live variant's pointer costs no depth |
| pointers | non-nil by default (nil with `-nil-inputs`); recursive types through a bounded type table |
| maps | nil (with `-nil-inputs`) or up to `maxlen` entries, keys mostly from the strings the call tree uses as map keys |
| `interface{}` | nil, string, bool, int, `map[string]interface{}`, `[]interface{}`, and (at the top level) up to six concrete types the function's call tree asserts empty interfaces to (`x.(int64)`, a type switch's cases), so that every case can be taken; the PoC writes them typed (`uint8(0)`) |
| interfaces with methods, funcs, channels, floats | their zero value (nil, 0) |
| `context.Context` | `context.Background()` |
| unexported fields of another package's struct | their zero value (a test cannot set them) |
| SDK response types (with JSON metadata) | generated field by field plus a raw JSON object of one or two members keyed by strings the call tree compares with `==`, then written as the JSON document the harness builds for the value and decoded by the JSON model: typed fields, `RawJSON()`, per-member `respjson.Field` and `ExtraFields` agree as decoding makes them; the harness decodes the same document |
| documented enums (`// Any of ...`) | one of the values three times in four; the values the call tree compares make half of the choices |
| pointers to another package's struct with an unexported field, whose package exports a function returning one (a handle) | opaque non-nil (the PoC passes a zero one: a panic through it is inconclusive); a field read through it is havoc, a store into it ignored; a struct of exported fields is generated field by field |
| receiver of a method whose type has a constructor (`NewT`, `newT`; for an unexported `t` also `NewT`/`newT` with its first letter capitalised) | built by it (`T.M~NewT`); when no wrapper can be built (a variadic constructor, parameters the model cannot generate), the method of an unexported type is no target: only its constructor makes one |

## 8. Not modelled (rejected functions)

Goroutines, `select` and channel operations (so `range` over a channel);
`defer` of a modelled function; string orderings (`<` on strings);
`unsafe` conversions; parameters of type `*testing.T` (test helpers);
generic functions as targets (their instances are not targets either:
a harness cannot name them).

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
