import json, os, sys, collections
d = sys.argv[1]
cwe = collections.Counter(); cwe_hi = collections.Counter(); years = collections.Counter()
n = nhi = nocwe = 0
for f in os.listdir(d):
    if not f.startswith("GHSA"): continue
    j = json.load(open(os.path.join(d, f)))
    if j.get("withdrawn"): continue
    ds = j.get("database_specific", {})
    sev = ds.get("severity", "")
    ids = ds.get("cwe_ids", []) or []
    n += 1
    hi = sev in ("HIGH", "CRITICAL")
    nhi += hi
    if not ids: nocwe += 1
    for c in set(ids):
        cwe[c] += 1
        if hi: cwe_hi[c] += 1
    years[(j.get("published") or "")[:4]] += 1
print(f"GHSA advisories: {n}, high/critical: {nhi}, without CWE: {nocwe}")
print("years:", sorted(years.items())[-8:])
for c, k in cwe.most_common(40):
    print(f"{c:10s} {k:5d}  high/crit {cwe_hi[c]:4d}")
