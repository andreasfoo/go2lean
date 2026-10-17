# go2lean

Find bugs in Go functions by translating them into an executable semantic
model in Lean 4, searching that model for counterexamples, and verifying every
counterexample with the real Go toolchain.

```
Go source ─► go/ssa ─► Lean SSA model ─► counterexample ─► go test ─► verdict
  (M1 programmatic)         (M2 constraint / agent)          (M3 poc)
```

The design is in [docs/design.md](docs/design.md).

## Quick start

Requirements: Go 1.26, [elan](https://github.com/leanprover/elan) (the
toolchain in `lean/lean-toolchain` is installed on first `lake build`).

```sh
go run ./cmd/go2lean run ./examples/overflow
```

```
== M1 compile
compiled github.com/andreasfoo/go2lean/examples/overflow -> lean/Go2Lean/Generated/gossa/overflow.lean
  SpecSum      complete  func(xs []int, ret int) bool  [spec for Sum]
  Sum          complete  func(xs []int) int
== M2 properties
  p_sum_nopanic                no_panic       Sum does not panic on any input
  p_sum_specsum                postcondition  Sum satisfies the spec-in-Go oracle SpecSum
== M2 hunt
  p_sum_nopanic                no counterexample in 2000 trials (0 out of fuel)
  p_sum_specsum                found at trial 47 -> cx_sum_specsum_s42
      inputs map[xs_cap:5 xs_len:4 ...] memory map[xs:[1 9223372036854775806 9223372036854775807 0]] model ok -2
== M3 poc
  cx_sum_specsum_s42           confirmed    real: map[ret:-2]
      PoC: out/poc/cx_sum_specsum_s42
```

The PoC directory holds a ready-to-run Go test and a README with the
reproduction command. Records exchanged between the modules are JSON files
under `out/` (see `internal/contract`).

## Writing properties

Two property shapes are templated automatically:

- `no_panic` for every modelled function;
- `postcondition` for every **spec-in-Go** function: a Go function with a
  `// go2lean:spec Target` doc comment and signature
  `func Spec(<Target params>..., ret <Target result>) bool`. It is compiled
  into the model with the same pipeline as the target, so the Lean property
  and the Go oracle agree by construction.

See `examples/` for the ground-truth packages (`overflow`, `oob`, `divzero`).

## Commands

```
go2lean compile [-workspace out] <package>        Go -> Lean model, ABI, targets
go2lean props   [-workspace out]                  template properties from the model report
go2lean hunt    [-workspace out] [-property id] [-seed n] [-trials n] [-fuel n] [-maxlen n]
go2lean poc     [-workspace out] -counterexample id
go2lean run     [-workspace out] <package>        all of the above
```

## Supported Go subset (stage A)

Integers of every width, bools, read-only slices of integers, calls between
functions in the same package, `len`/`cap`, reslicing. Loads of slice elements
read a per-run memory oracle; stores mark the function `partial`; unmodelled
calls become havoc values. Anything else rejects the function with a reason in
`out/models/gossa/report.json`.

## Layout

```
cmd/go2lean/           CLI
internal/contract/     shared JSON records and workspace
internal/frontend/     M1 frontends (go/ssa now; LLVM via TinyGo planned)
internal/ir, ssaout/   program representation and Lean printer
internal/constraint/   M2 core: property templates and engines
internal/poc/          M3: harness generation, execution, verdicts
lean/                  Lean 4 project: SSA core, GoSSA dialect, search engine
examples/              ground-truth packages
```

## License

MIT, see [LICENSE](LICENSE).
