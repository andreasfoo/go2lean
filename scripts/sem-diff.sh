#!/bin/bash
# Differential check of the model against Go on Goose's semantics tests
# (testdata/goosesem, #58). Every test returns true in Go; the model must
# agree. A test whose model outcome is not a clean "exhausted" (a
# counterexample, a havoc read, an assumption) is a known disagreement and
# must be listed in testdata/goosesem/expected.txt, with the issue that
# tracks it; any change, better or worse, fails until the list is updated.
# usage: scripts/sem-diff.sh <workspace> [-update]
set -euo pipefail
cd "$(dirname "$0")/.."
WS=${1:?workspace}
rm -rf "$WS"; mkdir -p "$WS"
go test -count=1 ./testdata/goosesem > "$WS/go.log"
go build -o "$WS/g2l" ./cmd/go2lean
"$WS/g2l" run -deps -specs-only -budget 0 -prove=false -workspace "$WS" ./testdata/goosesem > "$WS/run.log" 2>&1
python3 scripts/sem-diff.py "$WS" > "$WS/outcomes.txt"
expected=testdata/goosesem/expected.txt
if [ "${2:-}" = -update ]; then
  # Keep each line's comment (the issue it is tracked in).
  python3 - "$expected" "$WS/outcomes.txt" <<'PY'
import sys
old = {}
for l in open(sys.argv[1]) if __import__("os").path.exists(sys.argv[1]) else []:
    body, _, note = l.rstrip("\n").partition("  # ")
    old[body] = note
out = [l.rstrip("\n") for l in open(sys.argv[2])]
with open(sys.argv[1], "w") as f:
    for l in out:
        f.write(l + ("  # " + old[l] if old.get(l) else "") + "\n")
PY
  exit 0
fi
if diff -u <(sed 's/  # .*//' "$expected") "$WS/outcomes.txt"; then
  n=$(ls "$WS/properties" | grep -c '_goosesem_seq.*_sequence\.json$' || true)
  echo "sem-diff: $(grep -c . "$WS/outcomes.txt") known disagreements of $n tests, as expected"
else
  echo "sem-diff: outcomes differ from $expected (- expected, + now); log: $WS/run.log"
  exit 1
fi
