#!/usr/bin/env python3
"""Per case of evaluation 7: arm A's outcome (status, reason, model panic,
havoc sites resolved through the model reports, assumptions) and arm B's
failure message, for the problem ledger."""
import glob, json, os, re, sys, tarfile, collections
R = sys.argv[1]
def reports(ws):
    out = {}
    t = os.path.join(ws, "models.tgz")
    if os.path.exists(t):
        with tarfile.open(t) as tf:
            for m in tf.getmembers():
                if m.name.endswith("report.json"):
                    r = json.load(tf.extractfile(m)); out[m.name.split("/")[-2]] = r
    for f in glob.glob(os.path.join(ws, "models/gossa/*/report.json")):
        out[f.split("/")[-2]] = json.load(open(f))
    return out
def site(reps, h):
    pkg, _, i = h.get("origin", "").partition("#")
    r = reps.get(pkg.removeprefix("gossa."))
    try:
        s = r["havoc"][int(i)]; return s["reason"] + ": " + s["site"][:110]
    except Exception:
        return "havoc " + h.get("origin", "?")
for d in sorted(glob.glob(os.path.join(R, "k0*"))):
    cid = os.path.basename(d); ws = os.path.join(d, "wsA"); reps = reports(ws)
    print(f"== {cid}")
    seq = [json.load(open(f)) for f in glob.glob(ws + "/properties/*.json")]
    seq = [p["id"] for p in seq if p["id"].endswith("_sequence") and "seqconc" in p["id"]]
    vs = {}
    for f in glob.glob(ws + "/verdicts/*.json"):
        v = json.load(open(f)); c = json.load(open(ws + "/counterexamples/" + v["counterexample"] + ".json"))
        vs[c["property"]] = (v, c)
    for p in seq:
        if p in vs:
            v, c = vs[p]; d_ = v.get("diagnosis") or {}
            mo = c.get("model_output") or {}
            hs = collections.Counter(site(reps, h) for h in c.get("havoc") or [])
            print(f"  A: {v['status']} {d_.get('reason','')} model={mo.get('res')}:{mo.get('panic') or mo.get('value')} havoc={sum(hs.values())}")
            for s, n in hs.most_common(4): print(f"     havoc x{n} {s}")
            for a in (c.get("assumptions") or [])[:3]: print(f"     assume {a[:140]}")
            for n_ in (d_.get("notes") or [])[:2]: print(f"     note {n_[:160]}")
        else:
            pr = glob.glob(ws + "/proofs/*.json")
            st = [json.load(open(f)) for f in pr]; st = [x for x in st if x["property"] == p]
            hu = glob.glob(ws + "/hunts/*.json"); hu = [json.load(open(f)) for f in hu]; hu = [x for x in hu if x["property"] == p]
            print(f"  A: no counterexample; proof={st[0]['status'] if st else '-'}; {(hu[0]['summary'][:200] if hu else '')}")
            for a in (st[0].get("assumptions") or [])[:3] if st else []: print(f"     assume {a[:140]}")
    for f in glob.glob(d + "/wsB/*.json"):
        b = json.load(open(f)); msg = (b.get("message") or "").strip().splitlines()
        first = next((l for l in msg if "DATA RACE" in l or "panic" in l or "deadlock" in l or "false" in l or "timed out" in l or "hung" in l), msg[0] if msg else "")
        print(f"  B: {b['status']} {first[:150]}")
