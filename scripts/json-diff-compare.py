"""Compare the model's probe with the real return value of every
counterexample json-diff.sh verified. usage: json-diff-compare.py <workspace>"""
import glob
import json
import os
import sys

ws = sys.argv[1]
seeds = int(sys.argv[2]) if len(sys.argv) > 2 else 1
if seeds < 1:
    sys.exit("seeds must be positive")
properties = {
    os.path.basename(f)[:-5]
    for f in glob.glob(f"{ws}/properties/*diff.json")
}
if not properties:
    sys.exit("no differential properties found")
coverage = {p: set() for p in properties}
same = diff = other = 0
for f in sorted(glob.glob(f"{ws}/counterexamples/*.json")):
    cx = json.load(open(f))
    prop = cx.get("property")
    if prop not in properties:
        continue
    verdict = f"{ws}/verdicts/{os.path.basename(f)}"
    if not os.path.isfile(verdict):
        other += 1
        print("MISSING verdict", os.path.basename(f))
        continue
    v = json.load(open(verdict))
    model = [p["value"] for p in cx.get("probes") or []]
    observed = v.get("observed") or {}
    # An empty return value is not recorded: no "ret" without a panic is "".
    real = observed.get("ret", None if "panic" in observed else "")
    inp = json.dumps(cx.get("objects") or cx.get("inputs"))[:300]
    if v["status"] != "confirmed" or not model:
        other += 1
        print("OTHER", v["status"], inp)
    elif model[0] != real:
        diff += 1
        print("DIFF", inp, "\n  model:", model[0][:300], "\n  real: ", str(real)[:300])
    else:
        same += 1
        # Count distinct requested seeds, not duplicate artifacts.
        for seed in range(1, seeds + 1):
            if cx.get("id") == "cx_" + prop.removeprefix("p_") + f"_random_s{seed}":
                coverage[prop].add(seed)
        print("SAME", inp[:160], "->", model[0][:160])
print(f"same {same}, different {diff}, other {other}")
missing = {p: len(seen) for p, seen in coverage.items() if len(seen) < seeds}
for p, n in sorted(missing.items()):
    print(f"MISSING coverage {p}: {n} confirmed comparisons, expected at least {seeds}")
sys.exit(1 if diff or other or missing or not same else 0)
