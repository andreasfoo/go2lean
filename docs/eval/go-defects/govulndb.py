import json, os, sys, re, collections
d = sys.argv[1]
rules = [
 ("panic/crash on input", r"\bpanic|crash|nil pointer|index out of range|out of bounds|divide by zero"),
 ("unbounded memory/CPU", r"excessive|unbounded|exhaust|memory consumption|cpu consumption|denial of service|infinite loop|quadratic|resource consumption|large amount|out of memory|\boom\b"),
 ("authz/authn", r"authoriz|authenticat|bypass|privilege|permission|access control|unauthori"),
 ("injection/smuggling/SSRF", r"injection|smuggl|xss|cross-site|ssrf|request forgery|redirect|header|escap|sanitiz"),
 ("path traversal/file", r"path traversal|directory traversal|symlink|arbitrary file|zip slip|file write|file read"),
 ("crypto/TLS/verification", r"signature|verif|certificate|tls|crypto|timing|nonce|forg"),
 ("parsing/validation mismatch", r"pars|validat|mismatch|incorrect|improper|confus|inconsisten|normaliz"),
 ("info leak", r"leak|expos|disclos|sensitive"),
 ("race", r"\brace\b|concurren"),
]
cnt = collections.Counter(); n = 0; std = collections.Counter(); stdn = 0
for f in os.listdir(d):
    if not f.startswith("GO-"): continue
    j = json.load(open(os.path.join(d, f)))
    if j.get("withdrawn"): continue
    n += 1
    t = (j.get("summary","") + " " + j.get("details",""))[:2000].lower()
    pk = " ".join(a.get("package",{}).get("name","") for a in j.get("affected",[]))
    isstd = pk.startswith("stdlib") or "golang.org/x/" in pk or pk.startswith("toolchain")
    stdn += isstd
    hit = [k for k, r in rules if re.search(r, t)] or ["other"]
    first = hit[0]
    cnt[first] += 1
    if isstd: std[first] += 1
print("Go vulndb entries:", n, " stdlib/x:", stdn)
for k, v in cnt.most_common():
    print(f"{k:30s} {v:5d} {100*v/n:5.1f}%   stdlib/x {std[k]:4d} ({100*std[k]/max(stdn,1):.0f}%)")
