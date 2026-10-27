# Built-in semantic checks: implementation checkpoint

Status: internal source-generation/static-compile prototype, **not enabled by the CLI**.
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

`Binding.Validate` checks the supported card, domain and assumptions as well
as the source hash and canonical generated source. The source includes the
package/function binding. Substituting another function/package, changing the
law, or replacing it with arbitrary rehashed Go code does not validate as this
builtin.

## Static compiler capability gate

`internal/builtin.AssessCompareOrder` walks the generated target, oracle,
selected comparator, and all statically called local/cross-package callees.
It reads the compiler IR's actual call terminators and normal control-flow
edges, not the literal-mining call summaries in model reports. Missing,
partial or rejected callees, reported model issues, normal-path havoc or
assumptions, unresolved function/interface calls, concurrent operations,
unstructured IR terms and unreviewed primitive/operation types are unsupported.
Unrelated functions do not disqualify the selected call graph.

The only positive status is `static-model-eligible`, **not passed**. The
gate does not establish model fidelity, purity, determinism, totality,
termination or any ordering law. Recursive direct-call graphs can be statically
eligible without establishing that execution terminates.

Direct-call fallback blocks are synthetic paths for a callee that gets stuck.
The gate checks the callee and its normal continuation, and records the number
of fallback sites. A fallback reached through an ordinary branch is inspected
and its havoc is rejected. Excluding synthetic fallback edges from this static
walk does **not** establish that no search trial can take them; independent
runtime stuck/havoc/assumption/fuel accounting is still required.

The internal pipeline can request this gate with `Env.CompareOrder` alongside
the binding's canonical overlay. `CompileAll` and `CompileLinked` reject a
missing/mismatched/ambiguous generated overlay before loading source, assess
the actual compiler output, and stop before model emission when unsupported.
They preserve the card, full generated source and structured assessment at
`<workspace>/builtins/compare-order-<source-sha256>.json`, including unsupported
reasons. This is a diagnostic snapshot, **not** an active property, immutable
target-source/model snapshot, resume record, or executable replay manifest.
Ordinary compilation does not activate the gate.

Local external-source checkpoint (2026-10-27): a scratch clone of
`golang.org/x/mod` v0.42.0, revision
`b5136e5ba156da612a1efc6e49b55988a134a5e0`, was compiled with the generated
binding for `semver.Compare` and dependency models enabled. The gate reported
13 required functions as statically eligible and recorded 24 direct-call
fallback sites. The checkout remained unchanged. Unrelated partial functions
in dependency packages remained visible in the compile reports but were not
part of this comparator's required call graph.

This checkpoint used an internal assessment driver, **not** a released CLI
check. It executed no target function, searched no property, confirmed no
defect and is not yet a negative regression. Attempting to compile the overlay
directly over the downloaded module cache was rejected by Go: source beneath
`GOMODCACHE` cannot be replaced through an overlay. Use a scratch clone/source
copy outside that cache; a type-compatible binding alone does not establish
that its source location is eligible for Go overlays.

## Verified here, and what remains

Unit tests check positive and negative binding cases, collisions, source
provenance, immutable overlay copies and absence of checkout writes.
The generated target and postcondition translate together through the existing
Go frontend on a trusted unit fixture. Real Go tests exercise the generated
oracle on correct lexicographic and equivalence-class comparators, unequal
negative/positive result magnitudes, and deliberately invalid reflexivity,
sign reversal, equivalence transitivity and strict-order transitivity.
Capability tests exercise missing/partial dependencies, actual cross-package
compiler output, ignored shared-state effects, recursion, dynamic calls,
normal versus synthetic fallback edges, and fail-closed handling of unknown
operations. Pipeline tests check both compilation paths, exact source/overlay
correspondence, diagnostic persistence, failure before model emission and
unchanged ordinary compilation behavior.

These are unit tests, **not external-project defect evidence** and not
Lean-versus-Go execution parity. The following work is still required before
this can count toward release acceptance:

1. CLI/pipeline opt-in, persisted bindings and consistent generated-source
   availability across compile, search, replay and resumed commands.
2. Use the static capability gate consistently when activating checks, and tie
   emitted model/source snapshots to persisted properties and resumed commands.
   The current compiler diagnostic does not establish support for arbitrary
   callees or justify trusting a stale capability report.
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
