#!/usr/bin/env bash
# Compile an existing external Go module with an installed binary while the
# checkout is hidden and networking disabled. Requires Linux and usable bwrap.
# This is a packaging gate, NOT a search/replay or full release gate.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
MODULE=$(cd "${1:?external module directory}" && pwd)
WS=$(mkdir -p "${2:?new result directory}" && cd "$2" && pwd)
case "$MODULE/" in "$ROOT/"*) echo 'module must be outside checkout' >&2; exit 1;; esac
case "$WS/" in "$ROOT/"*) echo 'results must be outside checkout' >&2; exit 1;; esac
mkdir -p "$WS/bin" "$WS/tmp" "$WS/go-build"
(cd "$ROOT" && GOBIN="$WS/bin" go install ./cmd/go2lean)
# Module dependencies must already be downloaded. All Go build writes stay in WS.
bwrap --ro-bind / / --dev /dev --proc /proc \
  --bind "$WS" "$WS" --tmpfs "$ROOT" --chdir "$MODULE" \
  --unshare-all --die-with-parent \
  --setenv TMPDIR "$WS/tmp" --setenv GOCACHE "$WS/go-build" \
  --setenv GO2LEAN_CACHE "$WS/core-cache" \
  "$WS/bin/go2lean" compile -workspace "$WS/compiled" ./... > "$WS/compile.log" 2>&1
printf 'installed compile passed (checkout hidden, no network): %s\n' "$WS/compile.log"
