import json, os, sys, re, collections
d = sys.argv[1]
fam = {
 "authz/authn": {"CWE-863","CWE-862","CWE-284","CWE-285","CWE-639","CWE-269","CWE-306","CWE-287","CWE-290","CWE-613","CWE-522","CWE-1220","CWE-266","CWE-250","CWE-288","CWE-305","CWE-307","CWE-620","CWE-640","CWE-565","CWE-302"},
 "path/file": {"CWE-22","CWE-23","CWE-24","CWE-36","CWE-59","CWE-73","CWE-732","CWE-276","CWE-377","CWE-379","CWE-61","CWE-434","CWE-552","CWE-367"},
 "resource DoS": {"CWE-400","CWE-770","CWE-789","CWE-834","CWE-835","CWE-407","CWE-1333","CWE-674","CWE-405","CWE-409","CWE-606","CWE-1050"},
 "injection/SSRF/redirect": {"CWE-79","CWE-78","CWE-89","CWE-94","CWE-77","CWE-74","CWE-918","CWE-601","CWE-88","CWE-90","CWE-91","CWE-643","CWE-917","CWE-1336","CWE-113","CWE-93","CWE-116","CWE-80","CWE-95","CWE-611","CWE-502","CWE-1321"},
 "info exposure": {"CWE-200","CWE-532","CWE-209","CWE-201","CWE-212","CWE-215","CWE-359","CWE-497","CWE-538","CWE-312","CWE-319","CWE-526"},
 "crypto/TLS/signature": {"CWE-295","CWE-345","CWE-347","CWE-327","CWE-326","CWE-330","CWE-338","CWE-354","CWE-208","CWE-203","CWE-924","CWE-297","CWE-325","CWE-916","CWE-328","CWE-323","CWE-1240"},
 "panic/memory": {"CWE-476","CWE-129","CWE-125","CWE-787","CWE-190","CWE-191","CWE-754","CWE-248","CWE-369","CWE-681","CWE-120","CWE-119","CWE-704","CWE-843","CWE-617","CWE-131","CWE-1284"},
 "race/TOCTOU": {"CWE-362","CWE-366","CWE-367"},
 "logic/validation": {"CWE-20","CWE-697","CWE-436","CWE-444","CWE-1286","CWE-1287","CWE-1288","CWE-179","CWE-180","CWE-178","CWE-1023","CWE-184","CWE-183","CWE-185","CWE-670","CWE-682","CWE-841","CWE-115","CWE-1289"},
 "CSRF/session/web": {"CWE-352","CWE-346","CWE-1021","CWE-1275","CWE-384","CWE-565","CWE-614","CWE-942","CWE-1004"},
}
cnt = collections.Counter(); hi = collections.Counter(); n = 0
panic_words = collections.Counter()
recent = collections.Counter()
for f in os.listdir(d):
    if not f.startswith("GHSA"): continue
    j = json.load(open(os.path.join(d, f)))
    if j.get("withdrawn"): continue
    n += 1
    ds = j.get("database_specific", {})
    ids = set(ds.get("cwe_ids", []) or [])
    sev = ds.get("severity","") in ("HIGH","CRITICAL")
    fams = {k for k, v in fam.items() if ids & v} or {"other/none"}
    for k in fams:
        cnt[k] += 1; hi[k] += sev
        if (j.get("published") or "")[:4] in ("2025","2026"): recent[k] += 1
    text = (j.get("summary","") + " " + j.get("details",""))[:3000].lower()
    if "panic" in text: panic_words["mentions panic"] += 1
    if re.search(r"\bnil pointer|nil dereference|index out of range|slice bounds", text): panic_words["runtime-panic terms"] += 1
print("GHSA Go advisories:", n)
for k, v in cnt.most_common():
    print(f"{k:24s} {v:5d} {100*v/n:5.1f}%  high/crit {hi[k]:5d}  2025-26 {recent[k]:5d}")
print(dict(panic_words))
