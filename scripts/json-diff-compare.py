"""Compare the model's probe with the real return value of every
counterexample json-diff.sh verified. usage: json-diff-compare.py <workspace>"""
import glob
import json
import os
import sys

ws = sys.argv[1]
same = diff = other = 0
for f in sorted(glob.glob(f"{ws}/verdicts/*.json")):
    v = json.load(open(f))
    cx = json.load(open(f"{ws}/counterexamples/{os.path.basename(f)}"))
    model = [p["value"] for p in cx.get("probes") or []]
    real = (v.get("observed") or {}).get("ret")
    inp = json.dumps(cx.get("objects") or cx.get("inputs"))[:300]
    if v["status"] != "confirmed" or not model:
        other += 1
        print("OTHER", v["status"], inp)
    elif model[0] != real:
        diff += 1
        print("DIFF", inp, "\n  model:", model[0][:300], "\n  real: ", str(real)[:300])
    else:
        same += 1
        print("SAME", inp[:160], "->", model[0][:160])
print(f"same {same}, different {diff}, other {other}")
