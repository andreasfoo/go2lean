# Built-in semantic checks: implementation checkpoint

Status: **experimental explicit-opt-in CLI mode**, not a release acceptance result.
A model-generated non-panic ordering violation has now been confirmed on
a real external project, including a detached helper-bundle replay and a
locally proposed fix comparison. The **full release gate remains unmet**:
this is not yet a completed acceptance evaluation.
See the [recorded evaluation](evaluations/ordering-natural-20261028.md).

## Experimental usage

Only select this role when the function is intended to be a pure,
deterministic, total ordering comparator on **all** byte strings. Neither its
name nor its signature establishes that contract.

```sh
# From a trusted scratch module, using a new dedicated workspace:
GOWORK=off go2lean run -compare-order Compare -workspace /tmp/new-order-check .
```

The tool supplies the law and helpers; no Go spec is handwritten. The opt-in
asserts role/domain assumptions, rather than discovering an arbitrary API's
business contract. The initial mode supports one package, native 64-bit Go
1.26+, empty `GOFLAGS`, no active `go.work`, no test targets and no additional
source overlays. Dependencies are modeled automatically; fuzz assertions are
not imported in this mode. Methods, bool-returning `Less` APIs and
restricted-domain comparators are not supported adapters.

Stages may run in separate processes:

```sh
GOWORK=off go2lean compile -compare-order Compare -workspace /tmp/new-order-check .
go2lean props -workspace /tmp/new-order-check -package example.com/module
go2lean hunt -workspace /tmp/new-order-check
go2lean poc -module /scratch/module -workspace /tmp/new-order-check \
  -counterexample <model-generated-id>
```

Use the actual import path in `props`. Search operates on the frozen model;
replay needs identical source/dependencies and build configuration, but can
restore helpers in a clone at a different path. `run -compare-order Compare
-resume` reuses an unchanged bound workspace. Changed source or recompilation
requires a fresh workspace, not overwriting models while retaining old results.

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

`ConfigureCompareOrder` adds explicit activation on top of this diagnostic
gate. A content-addressed `checks/` snapshot pins the canonical helper, static
assessment, source/test dependency hashes, module manifests, selected embedded
and native files, build configuration fingerprint, bundled Lean runtime,
generated models, reports, ABIs, targets and registry. The canonical property's
reference identifies that snapshot. Configuration values are fingerprinted,
not dumped as credential-bearing environment data.

The fingerprint probe uses offline metadata loading, a 45-second command
deadline and bounded output. Dependencies/toolchains must already be prepared.
It does not execute target functions; compilation/type-check/build tooling
still requires trusted source as described in the usage guide. Custom compiler
executables/system libraries and hostile filesystem races are not authenticated
by these hashes.

Search and replay reject changed model/property records or stripped generated
property provenance. Replay restores the canonical helper from the snapshot,
not the original process's overlay, and checks source fingerprints before and
after execution. Resumed hunts require the same snapshot; cached verdicts also
bind the concrete counterexample digest. These are reproducibility/integrity
checks, not signed attestations or an adversary-proof trust chain.
Ordinary caller-plausibility ranking is not applied to the generated wrapper:
a confirmed oracle violation still depends on the explicitly asserted API role,
not on the wrapper being called by the original application.

Local external-source checkpoint (2026-10-27): a scratch clone of
`golang.org/x/mod` v0.42.0, revision
`b5136e5ba156da612a1efc6e49b55988a134a5e0`, was compiled with the generated
binding for `semver.Compare` and dependency models enabled. The gate reported
13 required functions as statically eligible and recorded 24 direct-call
fallback sites. The checkout remained unchanged. Unrelated partial functions
in dependency packages remained visible in the compile reports but were not
part of this comparator's required call graph.

That static checkpoint used an internal assessment driver, **not** a released CLI
check. It executed no target function, searched no property, confirmed no
defect and is not yet a negative regression. Attempting to compile the overlay
directly over the downloaded module cache was rejected by Go: source beneath
`GOMODCACHE` cannot be replaced through an overlay. Use a scratch clone/source
copy outside that cache; a type-compatible binding alone does not establish
that its source location is eligible for Go overlays.

The experimental CLI subsequently compiled a frozen check for the same
external comparator and ran 1,000 seeded model trials: 1,000 checked, zero
vacuous/moot/stuck or out-of-fuel trials, and no counterexample. This remains
**model-only** evidence: no real-Go parity result, confirmed external defect,
fixed-revision comparison, or accepted negative release control. It does not
establish coverage of valid version strings.

## Verified here, and what remains

External candidate triage (2026-10-28), with a frozen seed/budget protocol
and no imported upstream test inputs, rejected both
`github.com/maruel/natural.Compare` at
`83eecaf9a40d7c4212e4518531111204418a4077` and
`github.com/fvbommel/sortorder.NaturalCompare` at
`edc3fa94c108b5d9a4ee8931e2920aea61647c87` before search. The former
required the native `internal/bytealg.abigen_runtime_cmpstring` bridge;
the latter required generic `cmp.Compare` instantiations. These are
unsupported results, not negative controls or passes.

The native string bridge now lowers to existing unsigned byte-string
comparisons and conditional selection of exactly -1, 0 or 1. It does not
model arbitrary assembly or bypass capability checks. The dedicated
`testdata/stringcmpdiff` differential suite compares six generated string
pairs (in both directions), plus a 10-by-10 fixed matrix including empty,
prefix, NUL, high-byte and invalid UTF-8 strings, with sandboxed real Go:
six matching probes, zero different or missing. This is bridge validation,
not external comparator parity or a defect finding. Generic `cmp.Compare`
remains unsupported.

After that bridge change, the frozen protocol found a violation in the
unchanged `maruel/natural` revision at zero-based trial 695, seed 42.
The real sandboxed Go result matched all five model outputs and violated
sign reversal without a panic. Removing the original checkout/workspace
paths and replaying the delivered bundle against another identical clone
confirmed it again. The original witness holds after a local subtraction
fix, and 32,000 bounded model trials found no further witness. These are
recorded observations, not an upstream fix, arbitrary-input equivalence,
or a proof of ordering. Source revisions, inputs, limitations and the local
patch are in the evaluation record.

A separate audit then regenerated all 2,000 seed-42 trials from unchanged
models and compared all five return values and the generated oracle result
with sandboxed Go: 2,000 matched, zero mismatches or panics. Structured
observations reported 1,997 holding and three violating trials, with no
havoc reads, assumptions, stuck or fuel exhaustion. This is sampled
ASCII/numeric-pool parity and a separate evaluation instrument, not
arbitrary-byte fidelity or shipped integrated search accounting.

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
Snapshot tests cover source/configuration drift, moved identical clones,
tampered model/property/manifest files and stale resume records. A mandatory
integration gate uses real Lean search on a planted unit comparator, then
confirms its non-panic law violation in sandboxed Go **without passing an
overlay or binding to replay**. Its witness is generated by search. This is
unit pipeline evidence, not a real external-project finding.

These are unit tests, **not external-project defect evidence** and not
Lean-versus-Go execution parity. The following work is still required before
this can count toward release acceptance:

1. Exercise the implemented opt-in, persistence and resumed commands in the
   real-project installed-binary protocol.
2. Establish actual target/model applicability: passing a static gate does not
   establish arbitrary callee fidelity or justify a signature-based contract.
3. Complete non-vacuity/search accounting and model-versus-Go parity evidence.
4. Use and independently verify the [helper-bundle delivery path](pipeline.md#generated-support-sources)
   for activated builtin findings. Its package-local infrastructure is unit
   tested without the original workspace/overlay, but no installed external
   builtin finding has passed that path yet.
5. Finish the external-project acceptance evaluation: the recorded finding
   and local fix comparison are a first positive, not a complete fixed-release
   and negative-control matrix. Inputs must remain model-generated rather
   than supplied historical counterexamples.
6. Installed-binary and clean-environment evaluation of that entire path.

Without the explicit flag, `run` still uses automatic panic templates,
imported fuzz assertions and handwritten Go specs. The experimental ordering
mode never silently activates based on a function's name.
