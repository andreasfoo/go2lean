# Usage guide

[← Back to README](../README.md)

[Quick start](#quick-start) · [The agent](#the-agent) · [Commands](#commands) · [On a real module](#on-a-real-module) · [C and LLVM IR](#c-and-llvm-ir)

## Quick start

### 1. Prerequisites

Requirements: Git, Go 1.26 and [elan](https://github.com/leanprover/elan). The Lean
toolchain pinned in `lean/lean-toolchain` is fetched on the first build.
Recommended on Linux: [bubblewrap](https://github.com/containers/bubblewrap)
(`apt install bubblewrap`), which isolates tests during the replay stage; without
it they run with resource limits only.

### 2. Clone and run the included example

```sh
git clone https://github.com/andreasfoo/go2lean.git
cd go2lean
go run ./cmd/go2lean run ./examples/overflow
```

No LLM API key is needed. This example includes a Go spec: when all inputs
are non-negative, the sum should be non-negative. Integer overflow can
violate that rule without panicking.

### 3. Inspect the result

Illustrative output (search details may vary):

```
== Compile stage
compiled github.com/andreasfoo/go2lean/examples/overflow -> out/lean/Go2Lean/Generated/gossa/overflow.lean
  SpecSum      complete  func(xs []int, ret int) bool  [spec for Sum]
  Sum          complete  func(xs []int) int
== Spec & search stage: properties
  p_overflow_sum_nopanic       no_panic       Sum does not panic on any input
  p_overflow_sum_specsum       postcondition  Sum satisfies the spec-in-Go oracle SpecSum
== Spec & search stage: hunt
  p_overflow_sum_nopanic       random: none in 2000 trials; exhaustive: holds on 9724 inputs (recorded as exhausted)
  p_overflow_sum_specsum       random: found at trial 12 -> cx_overflow_sum_specsum_random_s42
      inputs map[xs_len:5 ...] memory map[xs:[1 0 0 0 9223372036854775807]] model ok -9223372036854775808
== Replay stage: generate PoC and verify
  cx_overflow_sum_specsum_random_s42  confirmed    real: map[ret:-9223372036854775808]
      PoC: out/poc/cx_overflow_sum_specsum_random_s42
```

The PoC directory holds a ready-to-run Go test and a README with the
reproduction command. Everything the modules exchange is a JSON record under
`out/` (types in `internal/contract`).

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
`round-2.md`, …) for tests and offline runs. Replace `model_name` below with
a model ID supported by your chosen endpoint.

```sh
go run ./cmd/go2lean agent -model model_name -base-url https://api.minimaxi.com/anthropic ./examples/oob
```

Every prompt and response is
written to `out/agent/<package>/`, and the report lands in `out/agent/<package>/report.md`. A
revised spec file in the report round triggers one more pass.

## Commands

```text
go2lean compile [-workspace out] <package>        Go -> Lean model, ABI, targets
go2lean props   [-workspace out] -package <path>  template properties from the model report
go2lean hunt    [-workspace out] [-property id] [-engine random|exhaustive|all] [-jobs n] [-budget s]
                [-seed n] [-trials n] [-fuel n] [-maxlen n] [-exhaust-len n] [-exhaust-limit n]
go2lean poc     [-workspace out] -counterexample id [more ids...]   one go test per package
go2lean run     [-workspace out] [-nil-inputs] <packages...>   compile, props, hunt, poc (./... allowed)
go2lean agent   [-workspace out] [-llm claude|mock:<path>] [-model id] [-base-url url] [-thinking]
                [-repairs n] [-report] <package>
```

## On a real module

```sh
go run ./cmd/go2lean compile -workspace out-tingly -module ../tingly-box ./...   # 156 packages, 15 s
go run ./cmd/go2lean agent -workspace out-tingly -module ../tingly-box -model model_name \
    -base-url https://api.minimaxi.com/anthropic github.com/tingly-dev/tingly-box/remote/scenario/builtin/claudecode
```

On tingly-box (7539 functions and methods) the model covers 7112; a full
pass over its 156 packages (7095 properties) takes about three hours at
four jobs and ends with 140 confirmed findings (80 of them not involving an
input the test could only pass as nil), 152 spurious counterexamples and
2779 exhaustive certificates (see docs/history.md, §9b): `truncate(s, n)`
helpers that panic on a negative `n`, `strings.Repeat` with a negative
count, writes to nil maps, slices of short hashes, and many nil
dereferences of fields a caller is expected to set. The agent on
`internal/typ` wrote 63 specs and found `pickWithinTier` indexing an
empty slice.

The tingly-box case study (hand-written specs, the rounds of hunting,
PoCs, full-pass history and one page per finding) lives in its own
repository, [andreasfoo/go2lean-tingly-box](https://github.com/andreasfoo/go2lean-tingly-box).
Its first finding: a hand-written postcondition on
`ConvertAnthropicBetaToOpenAIRequest` is refuted by the search and confirmed by the replay stage:
tool_use ids of 43 to 51 characters reach the assistant `tool_calls[].id`
untruncated while the tool-result side is truncated to 40 (tingly-box
issue #1924).

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
[docs/design.md §4.4](../docs/design.md).
