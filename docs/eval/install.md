# Standalone installation evaluation (in progress)

This is packaging evidence, not a release acceptance or a bug-finding evaluation.

On 2026-10-27, an installed Go binary compiled the external module
`github.com/google/uuid` at `2d3c2a9cc518326daf99a383f07c4d3c44317e4d`
with the entire go2lean checkout hidden by a bubblewrap tmpfs mount and
networking disabled. The empty go2lean runtime cache was populated from
embedded Lean sources, not the target module or a checkout. The compile
reported complete, partial and rejected functions; this does not mean all
of uuid is supported or verified.

Reproduction (download target dependencies before disabling networking):

```sh
git clone https://github.com/google/uuid.git /tmp/uuid
(cd /tmp/uuid && git checkout 2d3c2a9cc518326daf99a383f07c4d3c44317e4d && go mod download)
scripts/install-smoke.sh /tmp/uuid /tmp/go2lean-install-smoke
```

The script retains the installed binary, generated model, cache, offline
dependency/prerequisite diagnostic (`doctor.log`) and compile
log in its second argument. It rebuilds with `go install` from the current
source; validating installation from a published version remains a release
gate. Go's toolchain and downloaded dependencies remain readable on the host;
this is not a clean-machine dependency-installation test.
The result directory must not already exist, so stale artifacts or a populated
runtime cache cannot stand in for a fresh packaging run. CI runs this packaging
gate on the pinned uuid revision, independently of the sandbox integration gate.

The packaging run was repeated on 2026-10-27 after the environment-isolation
changes, using a new result directory (`/tmp/go2lean-env-scrub-install`).
Compilation again succeeded with the checkout hidden and networking disabled.
A second invocation on that same directory was rejected before installation,
as intended. The strict sandbox integration suite also passed with no skips;
its negative run with `GO2LEAN_LEAN_SANDBOX=off` failed rather than passing
without confinement. These are local results, not a claim of remote CI success.

With `doctor -deps` added to the installed packaging gate, a fresh run in
`/tmp/go2lean-deps-install` on the same date passed both diagnostics and
compilation. The diagnostic inspected 143 locally available packages, including
test dependencies. Go reported a failed attempt to write its module stat cache
on the read-only host filesystem; the warning was retained in `doctor.log`,
and no package-loading errors were reported. This is not an installation test
with absent toolchains or dependencies.

The final diagnostic-context build was installed and passed the same uuid
gate in `/tmp/go2lean-deps-context-install`. As a separate diagnostic negative,
that installed binary inspected go2lean's own CLI package with an empty module
cache: it failed with seven distinct offline missing-dependency diagnostics,
including package names and import stacks. Hashes of `go.mod` and `go.sum`
were unchanged. This validates missing-cache reporting, not the real-project
non-panic bug/fix negative-control requirement.

Local prerequisite diagnostics now exist (`go2lean doctor`), including a
probe for the comparator's bubblewrap capabilities. On the evaluation host,
bubblewrap 0.4.0 allowed basic replay confinement but lacked `--clearenv`;
the first proof judge run rejected results because its sandbox could not
start. This is an environment failure, not a successful proof regression.
The host was upgraded to bubblewrap 0.11.0 without disabling confinement.
The post-environment-scrubbing `scripts/proofs.sh` run completed successfully
on 2026-10-27 (`/tmp/go2lean-env-scrub-proofs`): five direct kernel-checked
theorems and 24 judged goals across four suites. All 24 `results.json` records
were inspected: `proved: true`, `sandbox: bwrap`, and
`judge: lake comparator --paranoid`. This establishes this proof regression,
not real-project bug-finding acceptance or the entire release gate.

Still required: complete dependency and safe execution acceptance, a spec-free
non-panic check with model-generated inputs and real Go confirmation,
real-project before/after and negative evaluations, complete regression
and clean-machine installation tests, and publication. No tag has been made.
