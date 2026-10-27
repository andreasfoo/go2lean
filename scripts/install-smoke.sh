#!/usr/bin/env bash
# Diagnose and compile an external Go module with an installed binary while the
# checkout is hidden and networking disabled after dependency provisioning.
# Requires Linux, usable bwrap, and a preinstalled Go/Lean toolchain.
# Optional explicit ordering search/replay is still NOT a full release gate.
set -euo pipefail
usage() {
  echo 'usage: install-smoke.sh <external-module> <new-results-directory> [--require-external-deps] [--compare-order Function]' >&2
  exit 2
}
(( $# >= 2 )) || usage
MODULE_ARG=$1
RESULT_ARG=$2
shift 2
REQUIRE_DEPS=
COMPARE_ORDER=
while (( $# )); do
  case "$1" in
    --require-external-deps)
      [[ -z "$REQUIRE_DEPS" ]] || usage
      REQUIRE_DEPS=1
      shift ;;
    --compare-order)
      (( $# >= 2 )) || usage
      [[ -z "$COMPARE_ORDER" && "$2" =~ ^[a-zA-Z_][a-zA-Z_0-9]*$ ]] || usage
      COMPARE_ORDER=$2
      shift 2 ;;
    *) usage ;;
  esac
done
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
MODULE=$(cd "$MODULE_ARG" && pwd -P)
# Never reuse an old binary, generated workspace or populated runtime cache as
# evidence of a fresh install. mkdir (without -p) also rejects concurrent reuse.
mkdir -- "$RESULT_ARG"
WS=$(cd "$RESULT_ARG" && pwd -P)
case "$MODULE/" in "$ROOT/"*) echo 'module must be outside checkout' >&2; exit 1;; esac
case "$WS/" in "$ROOT/"*) echo 'results must be outside checkout' >&2; exit 1;; esac
case "$MODULE/" in "$WS/"*) echo 'module and results must not overlap' >&2; exit 1;; esac
case "$WS/" in "$MODULE/"*) echo 'module and results must not overlap' >&2; exit 1;; esac
mkdir -p "$WS/bin" "$WS/tmp" "$WS/go-build" "$WS/home" \
  "$WS/install-modules" "$WS/install-go-build" "$WS/target-modules"
# Toolchains are explicit prerequisites, not evidence of fresh installation.
# Do not inherit credentials, Go settings, cached modules or user config.
GOROOT=$(env -i "PATH=$PATH" "HOME=$WS/home" GOENV=off \
  GOTOOLCHAIN=local go env GOROOT)
ELAN_HOME=${ELAN_HOME:-"$HOME/.elan"}
CLEAN_ENV=(
  "PATH=$PATH" "HOME=$WS/home" "LANG=C.UTF-8"
  "GOROOT=$GOROOT" "ELAN_HOME=$ELAN_HOME"
  "TMPDIR=$WS/tmp" "GOPATH=$WS/go" "GOENV=off" "GOWORK=off"
  "GOTOOLCHAIN=local" "GOTELEMETRY=off"
)
(cd "$ROOT" && env -i "${CLEAN_ENV[@]}" \
  GOBIN="$WS/bin" GOMODCACHE="$WS/install-modules" \
  GOCACHE="$WS/install-go-build" GOFLAGS=-mod=readonly \
  go install ./cmd/go2lean) > "$WS/install.log" 2>&1
# Both preparation and offline execution see read-only target files. Preparation
# may fetch dependencies into a NEW cache but cannot repair go.mod/go.sum.
isolated() {
  local network=$1
  shift
  local net_args=()
  if [[ "$network" == online ]]; then net_args=(--share-net); fi
  local env_args=()
  for pair in "${CLEAN_ENV[@]}"; do
    env_args+=(--setenv "${pair%%=*}" "${pair#*=}")
  done
  bwrap --ro-bind / / --dev /dev --proc /proc --clearenv \
    --bind "$WS" "$WS" --tmpfs "$ROOT" --chdir "$MODULE" \
    --unshare-all "${net_args[@]}" --die-with-parent \
    "${env_args[@]}" \
    --setenv GOCACHE "$WS/go-build" \
    --setenv GOMODCACHE "$WS/target-modules" \
    --setenv GO2LEAN_CACHE "$WS/core-cache" \
    "$@"
}
installed() {
  isolated offline env GOPROXY=off GOSUMDB=off GOVCS='*:off' \
    "$WS/bin/go2lean" "$@"
}
if [[ "$REQUIRE_DEPS" != "" ]]; then
  installed doctor > "$WS/prerequisites.log" 2>&1
  if installed doctor -deps ./... > "$WS/missing-deps.log" 2>&1; then
    echo 'expected missing external dependencies in the fresh target cache, but doctor passed' >&2
    exit 1
  fi
  # Go can fail before emitting per-package JSON when a module graph entry
  # itself is missing. Require the explicit offline-lookup diagnostic in that
  # case, not merely any go-list/tool failure.
  if ! grep -Eq '^FAIL +dependency ' "$WS/missing-deps.log" &&
     ! { grep -Eq '^FAIL +dependencies +go list failed:' "$WS/missing-deps.log" &&
         grep -Fq 'module lookup disabled by GOPROXY=off' "$WS/missing-deps.log"; }; then
    echo 'doctor failed without a missing-dependency diagnostic; inspect missing-deps.log' >&2
    exit 1
  fi
fi
isolated online go mod download > "$WS/dependencies.log" 2>&1
installed doctor -deps ./... > "$WS/doctor.log" 2>&1
installed compile -workspace "$WS/compiled" ./... > "$WS/compile.log" 2>&1
printf 'installed compile passed (fresh HOME and Go caches, checkout hidden, offline after dependency setup): %s\n' "$WS/compile.log"
if [[ -n "$COMPARE_ORDER" ]]; then
  # Explicit role assertion, not inferred API semantics. A successful command
  # (or no witness) is not a confirmed defect or a correctness certificate.
  order_args=(run -compare-order "$COMPARE_ORDER" -workspace "$WS/ordering"
    -engine random -seed 42 -trials 2000 -maxlen 8 -fuel 1000 -budget 60
    -mutate=false -prove=false)
  installed "${order_args[@]}" . > "$WS/ordering.log" 2>&1
  installed "${order_args[@]}" -resume . > "$WS/ordering-resume.log" 2>&1
  printf 'installed ordering search/replay and resume completed; inspect hunt/verdict evidence (not an acceptance verdict): %s\n' "$WS/ordering"
fi
