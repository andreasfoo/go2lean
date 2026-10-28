# One line per case: arm A's verdict on the spec property, arm B's fuzz result.
import glob, json, os, re, sys
B = os.path.dirname(os.path.abspath(__file__))
for d in sorted(glob.glob(f"{B}/{sys.argv[1] if len(sys.argv)>1 else 'results'}/c*")):
    cid = os.path.basename(d)
    ws = f"{d}/ws"
    props = {}
    for f in glob.glob(f"{ws}/properties/*.json"):
        p = json.load(open(f))
        if p.get("kind") != "no_panic":
            props[p["id"]] = p
    a = "no spec property"
    for pid in props:
        a = "none"
        h = f"{ws}/hunts/{pid}.json"
        if os.path.exists(h):
            hr = json.load(open(h))
            a = "none: " + re.sub(r"\s+", " ", hr["summary"])[:70]
            if hr.get("counterexample"):
                v = f"{ws}/verdicts/{hr['counterexample']}.json"
                if os.path.exists(v):
                    vd = json.load(open(v))
                    a = vd["status"] + ((" " + vd["diagnosis"]["reason"]) if vd.get("diagnosis") else "")
                else:
                    a = "cx, no verdict"
    blog = open(f"{d}/B.log").read() if os.path.exists(f"{d}/B.log") else ""
    m = re.search(r"^\s+p_\S+\s+(violated|held|error|timeout)\S*\s+(\d+)s", blog, re.M)
    if m:
        b = f"{m.group(1)} {m.group(2)}s"
    elif "skipped:" in blog:
        b = "skipped: " + blog.split("skipped:")[1].strip().splitlines()[0][:40]
    else:
        b = "error: " + (blog.strip().splitlines() or [""])[-1][:60]
    print(f"{cid}  A: {a:<55}  B: {b}")
