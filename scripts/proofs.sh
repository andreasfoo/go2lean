#!/bin/bash
# Check the functional proofs of lean/Proofs/Functional.lean against a freshly
# generated model of examples/proofs: compile the package into a workspace,
# then elaborate the proof file from it, prove examples/proofs/goals.json
# with go2lean prove, and check the agent's examples/invariants/proofs.json.
# Fails on an error, a `sorry`, or a
# proof that trusts more than the kernel (Lean.ofReduceBool).
# usage: scripts/proofs.sh <workspace>
set -e
cd "$(dirname "$0")/.."
ROOT=$(pwd)
WS=${1:?workspace}
rm -rf "$WS"; mkdir -p "$WS"
go build -o "$WS/g2l" ./cmd/go2lean
"$WS/g2l" compile -workspace "$WS" ./examples/proofs > "$WS/compile.log" 2>&1 || {
  cat "$WS/compile.log"; echo "proofs: compile failed"; exit 1; }
if ! (cd "$WS/lean" && lake build Go2LeanModels && lake lean "$ROOT/lean/Proofs/Functional.lean") > "$WS/proofs.log" 2>&1; then
  grep -v '^✔' "$WS/proofs.log" | head -60; echo "proofs: Lean rejected lean/Proofs/Functional.lean"; exit 1
fi
if grep -q 'sorryAx\|ofReduceBool' "$WS/proofs.log"; then
  grep 'depends on axioms' "$WS/proofs.log"; echo "proofs: a theorem uses sorry or trusts the compiler"; exit 1
fi
grep 'depends on axioms' "$WS/proofs.log"
echo "proofs: $(grep -c 'depends on axioms' "$WS/proofs.log") theorems checked by the kernel"
# The same functions' goals, stated and proved by go2lean prove alone.
if ! "$WS/g2l" prove -workspace "$WS/prove" -goals examples/proofs/goals.json ./examples/proofs > "$WS/prove.log" 2>&1; then
  tail -20 "$WS/prove.log"; echo "proofs: go2lean prove did not prove examples/proofs/goals.json"; exit 1
fi
grep '^  proved' "$WS/prove.log"
# The proof agent's proofs of loops (examples/invariants, issue #22), checked
# again without the agent.
if ! "$WS/g2l" prove -workspace "$WS/prove-inv" -goals examples/invariants/proofs.json ./examples/invariants > "$WS/prove-inv.log" 2>&1; then
  tail -20 "$WS/prove-inv.log"; echo "proofs: examples/invariants/proofs.json no longer proves"; exit 1
fi
grep '^  proved' "$WS/prove-inv.log"
