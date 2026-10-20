#!/bin/bash
# Differential check of go2lean's encoding/json model against Go: hunt the
# always-failing specs of testdata/jsondiff with several seeds, run every
# counterexample for real (M3), and compare each model probe with the real
# value. usage: scripts/json-diff.sh <workspace> [seeds]
set -e
cd "$(dirname "$0")/.."
WS=${1:?workspace}; SEEDS=${2:-12}
rm -rf "$WS"; mkdir -p "$WS"
go build -o "$WS/g2l" ./cmd/go2lean
"$WS/g2l" compile -workspace "$WS" ./testdata/jsondiff > "$WS/compile.log" 2>&1
"$WS/g2l" props -workspace "$WS" -package github.com/andreasfoo/go2lean/testdata/jsondiff > /dev/null || true
for p in $(ls "$WS/properties" | sed 's/\.json$//' | grep diff); do
  for seed in $(seq 1 "$SEEDS"); do
    "$WS/g2l" hunt -workspace "$WS" -property "$p" -seed "$seed" -engine random > /dev/null 2>&1 || true
  done
done
for cx in $(ls "$WS/counterexamples" | sed 's/\.json$//'); do
  "$WS/g2l" poc -workspace "$WS" -counterexample "$cx" >> "$WS/poc.log" 2>&1 || true
done
python3 scripts/json-diff-compare.py "$WS"
