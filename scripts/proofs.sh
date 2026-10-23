#!/bin/bash
# Check the functional proofs of lean/Proofs/Functional.lean against a freshly
# generated model of examples/proofs: compile the package into a workspace,
# then elaborate the proof file from it. Fails on an error, a `sorry`, or a
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
