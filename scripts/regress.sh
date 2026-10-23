#!/bin/bash
# Regression gate on the ground-truth packages: run the whole pipeline on
# ./examples/... and compare every property's outcome (the M3 verdict of its
# counterexample, or its certificate) with testdata/regress/examples.golden.
# Unlimited search budget (-budget 0): the outcome depends on the inputs
# only, not on how fast the machine is.
# usage: scripts/regress.sh <workspace> [-update]
set -e
cd "$(dirname "$0")/.."
WS=${1:?workspace}
rm -rf "$WS"; mkdir -p "$WS"
go build -o "$WS/g2l" ./cmd/go2lean
"$WS/g2l" run -deps -budget 0 -workspace "$WS" ./examples/... > "$WS/run.log" 2>&1 || {
  tail -40 "$WS/run.log"; echo "regress: go2lean run failed (log: $WS/run.log)"; exit 1; }
python3 - "$WS" > "$WS/outcomes.txt" <<'EOF'
import glob, json, os, sys
ws = sys.argv[1]
def load(d):
    return {os.path.basename(f)[:-5]: json.load(open(f)) for f in glob.glob(f"{ws}/{d}/*.json")}
out = {p: "none" for p in load("properties")}
for p, r in load("proofs").items():
    out[p] = r["status"]
for f in glob.glob(f"{ws}/verdicts/*.json"):
    c = os.path.basename(f)[:-5]
    cx = json.load(open(f"{ws}/counterexamples/{c}.json"))
    out[cx["property"]] = json.load(open(f))["status"]
muts = load("mutations")
# A bug only a rare interleaving shows: go test may or may not reproduce it
# (confirmed or inconclusive), so the gate records either as one outcome.
sched = set(open("testdata/regress/schedule-dependent.txt").read().split())
for p in sorted(out):
    short = p.removeprefix("p_github_com_andreasfoo_go2lean_examples_")
    if short in sched and out[p] in ("confirmed", "inconclusive"):
        out[p] = "schedule_dependent"
    m = muts.get(p)
    mut = f" mut={m['killed']}/{m['tried']}" if m else ""
    print(out[p] + mut, p.removeprefix("p_github_com_andreasfoo_go2lean_examples_"))
EOF
GOLDEN=testdata/regress/examples.golden
if [ "$2" = "-update" ]; then cp "$WS/outcomes.txt" "$GOLDEN"; echo "regress: updated $GOLDEN"; exit 0; fi
if diff -u "$GOLDEN" "$WS/outcomes.txt"; then
  echo "regress: $(wc -l < "$GOLDEN") properties as expected ($(grep -c '^confirmed' "$GOLDEN") confirmed)"
else
  echo "regress: outcomes differ from $GOLDEN (- expected, + now); log: $WS/run.log"
  grep -o 'prover: not proved.*' "$WS/run.log" | sort | uniq -c | head -20
  exit 1
fi
