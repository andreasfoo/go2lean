# go2lean report: examples/oob

## Confirmed bugs

- **`Last` panics on every non-empty slice** (`p_last_nopanic`). Input `xs` of length 6: the real run panics with `index out of range [6] with length 6`. The loop condition `i <= len(xs)` lets `i` reach `len(xs)-1` and the body then reads `xs[i+1]`. Fix: return `xs[len(xs)-1]` directly, or change the body to `return xs[i]`.

## Spurious counterexamples

- None.

## Properties that held

- `Clamp`: `no_panic`, `SpecClamp`, `SpecClampIdempotent`, `SpecClampIdentity` were certified by the exhaustive engine over boundary values and small magnitudes (domain in the proof records). No full proof yet.
- `SpecLastEmpty` and `SpecLastIsLast` could not be evaluated for non-empty inputs because `Last` panics first; they are moot until the panic is fixed.
