# Experimental natural-order evaluation, 2026-10-28

**Status: a real external non-panic finding, not a completed release gate.**
No report or patch has been submitted to the upstream project. This checks
functional sorting semantics, not exploitability or a security boundary.

## Selection and frozen protocol

Target: `github.com/maruel/natural`, revision
`83eecaf9a40d7c4212e4518531111204418a4077`, package-level `Compare`.
Its production documentation describes natural string ordering and its
`Less`/`StringSlice` adapters use it for sorting. It specifies no restricted
string grammar. This justifies explicitly selecting the ordering role; a
name/signature alone would not.

The tool supplies the existing `compare-order` v1 helper and law, with no
handwritten target spec. Purity, determinism and totality over all byte strings
remain explicit assumptions. Production source and commit subjects were
inspected before selection; upstream test payloads were not read or imported
into search. Neither the input below nor another historical trigger was fed
to the generator.

The first frozen protocol used the tool at `05da2a0` and rejected this target
before search because native string comparison was unmodeled. It also rejected
`github.com/fvbommel/sortorder.NaturalCompare` at
`edc3fa94c108b5d9a4ee8931e2920aea61647c87` for unsupported generic
`cmp.Compare`. Neither rejection is a negative-control pass.

Protocol v2 reran only `natural.Compare`, after adding the narrow native
string comparison bridge and validating it against Go. Search settings:

```text
engine=random seed=42 trials=2000 maxlen=8 fuel=1000 budget=60
mutate=false prove=false
```

The tool binary SHA-256 was
`d9ee0eb21d6a52183b51be07713d8b0f707addac5394d76096ab4ce7125c81ad`.
The accompanying protocol record must be consulted to verify that digest;
it identifies the locally built experimental tool, not a released binary.
`maxlen` is a generator setting, **not a hard bound on all emitted strings**:
the pre-existing generic numeric-boundary pool can emit longer strings.
No candidate-specific generator was added.

## Model-generated positive and real-Go confirmation

At zero-based trial **695**, search produced:

```text
a = "9223372036854775808"
b = "00-:"
c = "9Bb"
```

The generated helper evaluates `aa, ab, ba, bc, ac`. The model returned:

```text
0 -9223372036854775808 -9223372036854775808 -9 9223372036854775799
```

Sandboxed real Go returned exactly the same five integers and emitted
`panicked:false, spec:false`. In particular, both `Compare(a,b)` and
`Compare(b,a)` are negative, violating sign reversal. This is not a panic
finding, not an exit-code-only inference, and not merely a static capability
assessment. The source converts a wrapped unsigned subtraction into an
`int`; the ordering law itself never subtracts or negates comparator results.

Snapshot: `9fef9cc1b53b81c77c3a89570fb9192f842402f48906c3c9c477c45a93edcdb4`.
Counterexample digest:
`3f4a03468e8e0c8fcaea516d33df9cf7beae6aa9cc99ef3744e07a9599008d95`.
Selected machine-readable observations are in
[`ordering-natural-20261028/`](ordering-natural-20261028/).
These hashes are integrity identifiers, not signed certificates.

## Detached replay and negative packaging control

Copied only the delivered PoC bundle to a new directory and created another
clone of the exact target revision. Moved the original target and workspace
to evidence-retention locations so **neither original path existed**. They
were retained, not destroyed; this does not claim adversarial filesystem
inaccessibility during the build.

Ran the delivered standard-library-only `prepare.go` to create a fresh
overlay for the new clone, compiled with `go test -c`, and ran that binary
through `sandbox.Run`, requiring actual bubblewrap isolation. No original
in-memory overlay was supplied. The test again emitted the exact five
integers above, `panicked:false, spec:false`. The new clone remained clean.

A separate overlay with only the delivered harness and no helper failed
compilation. This demonstrates that the support-source delivery is necessary
and works here; it is a **packaging** negative control, not an external
correct-comparator negative control.

## Local candidate fix, not an upstream fixed release

Protocol v3 was recorded before running a new clone with this minimal patch:
[`local-fix.patch`](ordering-natural-20261028/local-fix.patch).
It replaces `int(an-bn)` with explicit less/greater branches.
It does not change the tool's oracle or feed the discovered inputs into search.

Replaying the already discovered witness on that clone with the same
delivered helper/harness produced:

```text
0 1 -1 -1 1
panicked:false, spec:true
```

The initial seed-42 settings above checked 2,000 trials with no witness.
The precommitted follow-up seeds 7, 19 and 2026 each checked 10,000 trials
with `maxlen=32 fuel=10000 budget=120`. All four logs reported zero
out-of-fuel, moot/stuck or vacuous trials. This is **32,000 bounded model
trials**, not 32,000 Go replays and not a proof that the proposed fix
establishes ordering on all strings.

## Separate model/Go parity audit

An exploratory audit subsequently regenerated **all 2,000 seed-42 trials**,
continuing past the first violation, using the frozen v2 search description
and unchanged generated model sources. This did not modify the bound
workspace, replace its oracle, inject a witness into search, or become a new
claim of spec-free bug discovery. The audit driver duplicates the existing
generator/check loop and records each trial rather than stopping on a finding.
The trial-695 inputs and output were checked against the saved counterexample.

For these 2,000 samples:

- The actual generated ordering oracle completed on every trial: 1,997
  returned true and three returned false.
- There were zero model havoc reads, assumptions, stuck or out-of-fuel
  outcomes. All samples had an oracle exit. The canonical oracle has no
  `return true` precondition guard, so these are not guard-vacuous successes.
- A generated Go audit test compared **all five integer results and the
  actual generated oracle result** for every sample against the recorded
  model observations. It ran through `sandbox.Run`, requiring bubblewrap.
  All 2,000 samples matched, with zero mismatches or panics.

The structured summary, sample-stream digest, unchanged model-source hashes,
audit driver, Go-test generator and sandbox runner are retained as
`parity-*` evidence files. The raw sample stream and generated 2,000-case test
remain in the local evidence directory rather than being checked into the
repository; the retained sources and frozen settings describe their
regeneration. The renderer/generator explicitly rejects non-ASCII sampled
inputs in this audit: this is **ASCII/numeric-pool sampled parity**, not
arbitrary-byte equivalence or Unicode coverage. The native bridge's fixed
byte-edge matrix is separate evidence, not broader target parity.

This also gives structured accounting for the positive-search prefix, but
it is a separate evaluation instrument. The shipped hunt records still do
not persist that per-trial accounting; an integrated fail-closed acceptance
evaluator remains work to do.

## Remaining acceptance work

The optional installed path also completed on the unchanged target:

```sh
scripts/install-smoke.sh /scratch/natural /new/results --compare-order Compare
```

This installs into new tool caches and a new HOME, prepares dependencies in
a separate initially empty target cache, hides the go2lean checkout, keeps
the target read-only and disables networking for doctor, compile, search,
replay and resume. Its seed-42 search independently found trial 695 again.
The saved verdict was inspected for `confirmed`, the exact five integers,
`panicked:false` and `spec:false`; resume reused the immutable snapshot and
cached search. See `installed-*.json` and `installed-*.log` in the evidence
directory. This validates the whole positive path under those conditions,
not a clean-machine OS/toolchain install or the broader release matrix.

- Integrate persisted, validated non-vacuity/termination accounting into the
  shipped search/evaluation records rather than relying on a separate audit.
- Broader byte-domain/Unicode target parity and coverage beyond these samples.
- A relevant independent external negative comparator control with semantic
  input coverage; the earlier `x/mod` model-only run is not yet one.
- Expand the clean installed-binary evaluation beyond this single positive.
- Run all current regression, proof and differential gates, review CI through
  a feature-branch PR, and resolve the release tracking requirements before
  publishing. No release has been created.
