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

The script retains the installed binary, generated model, cache and compile
log in its second argument. It rebuilds with `go install` from the current
source; validating installation from a published version remains a release
gate. Go's toolchain and downloaded dependencies remain readable on the host;
this is not a clean-machine dependency-installation test.

Local prerequisite diagnostics now exist (`go2lean doctor`), including a
probe for the comparator's bubblewrap capabilities. On the evaluation host,
bubblewrap 0.4.0 allowed basic replay confinement but lacked `--clearenv`;
the first proof judge run rejected results because its sandbox could not
start. This is an environment failure, not a successful proof regression.
The host was upgraded to bubblewrap 0.11.0 without disabling confinement.
Proof regression results are still pending.

Still required: complete dependency and safe execution acceptance, a spec-free
non-panic check with model-generated inputs and real Go confirmation,
real-project before/after and negative evaluations, complete regression
and clean-machine installation tests, and publication. No tag has been made.
