# Built-in semantic checks: implementation checkpoint

Status: internal source-generation prototype, **not enabled by the CLI**.
The release requirement for a spec-free non-panic check on a real external
project remains unmet. Source generation alone is neither a working pipeline
check nor release evidence.

## Explicitly bound comparison-order oracle

`internal/builtin.BindCompareOrder` generates a Go helper pair for a selected
package-level function with exact signature `func(string, string) int`. It
requires exactly one package and an explicit function identifier. It never
selects a function merely because it is called `Compare`, and it does not infer
the function's intended contract from that signature.

The intended role is the three-way ordering comparator documented by the
installed Go toolchain's `go doc slices.SortFunc`. The caller must assert that
the selected function is pure, deterministic, total over **all Go byte
strings**, and intended to define a strict weak ordering. Restricted input
grammars need a separate domain adapter; this prototype does not supply one.
Stateful functions, locale/environment-dependent behavior and intentional
non-order relations are not justified targets merely because they type-check.

For generated inputs `a, b, c`, the target helper records five actual comparator
results: `aa`, `ab`, `ba`, `bc`, `ac`. The postcondition helper requires:

- `aa == 0`;
- `ab` and `ba` have opposite signs, or are both zero;
- if `ab <= 0` and `bc <= 0`, then `ac <= 0`.

The relation includes transitivity of zero-equivalence; zero does **not**
require identical input bytes. Result magnitudes need not be one, and the
oracle does not negate or multiply results, avoiding overflow in its own
sign check. Purity and determinism are assumptions, not properties established
by these five observations.

Binding rejects methods, function-valued variables, variadics, generics,
named input/output types, incompatible result shapes, ambiguous package
selection and generated-name/file collisions. Aliases of builtin `string`
and `int` are allowed. It type-checks source rather than relying only on
export data, which can omit unused unexported declarations.

The binding records an ID/version, assumptions, domain, law, package/function,
generated target/oracle names, source path, full source and source SHA-256.
Its stage is explicitly `type-compatible-source-only`: **not** applicable-model,
searched, replayed, or passed. An overlay copy is returned without writing
generated source into the target checkout.

## Verified here, and what remains

Unit tests check positive and negative binding cases, collisions, source
provenance, immutable overlay copies and absence of checkout writes.
The generated target and postcondition translate together through the existing
Go frontend on a trusted unit fixture. Real Go tests exercise the generated
oracle on correct lexicographic and equivalence-class comparators, unequal
negative/positive result magnitudes, and deliberately invalid reflexivity,
sign reversal, equivalence transitivity and strict-order transitivity.

These are unit tests, **not external-project defect evidence** and not
Lean-versus-Go execution parity. The following work is still required before
this can count toward release acceptance:

1. CLI/pipeline opt-in, persisted bindings and consistent generated-source
   availability across compile, search, replay and resumed commands.
2. Capability checks for the full target/oracle call graph; type compatibility
   and one complete unit model do not establish support for arbitrary callees.
3. Complete non-vacuity/search accounting and model-versus-Go parity evidence.
4. Standalone delivery of required generated helpers, independently replayed
   without the original in-memory overlay or generated workspace.
5. A frozen authorized external-project evaluation with a real non-panic
   violation, fixed revision and relevant negative controls, using
   model-generated inputs rather than supplied historical counterexamples.
6. Installed-binary and clean-environment evaluation of that entire path.

The existing `run` behavior has not changed: its spec-free automatic templates
still provide panic checks; existing fuzz assertions and handwritten Go specs
remain separate mechanisms.
