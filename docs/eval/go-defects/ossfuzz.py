import json, os, sys, re, collections
d = sys.argv[1]
types = collections.Counter(); projs = collections.Counter(); n = 0; ex = {}
goish = re.compile(r"slice bounds out of range|index out of range|nil-dereference|go panic|runtime error|fatal error: |integer divide by zero|out-of-memory|timeout|stack-overflow", re.I)
for f in os.listdir(d):
    j = json.load(open(os.path.join(d, f)))
    det = j.get("details","")
    m = re.search(r"Crash type: (.*)", det)
    ct = m.group(1).strip() if m else "?"
    proj = (j.get("affected") or [{}])[0].get("package",{}).get("name","")
    # Go-only crash markers: slice bounds, Go panics
    if re.search(r"Slice bounds out of range|Index out of range|Nil-dereference|Go panic|Fatal error|Integer divide-by-zero|Integer divide by zero", ct, re.I):
        projs[proj] += 1
langs = {}
print(len(os.listdir(d)))
