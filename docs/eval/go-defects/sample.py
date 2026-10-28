import os, re, subprocess, random, sys, json
R = random.Random(20261028)
fixre = re.compile(r"\b(fix|fixes|fixed|bug|crash|panic)\b", re.I)
excl = re.compile(r"\b(docs?|readme|typo|lint|golint|vet|ci|travis|workflow|test(s)?|example|changelog|bump|deps?|dependenc|go\.mod|format(ting)?|comment|spelling|build|release|version)\b", re.I)
def pool(path):
    out = subprocess.run(["git","-C",path,"log","--no-merges","--format=@@%H %s","--name-only","HEAD"],capture_output=True,text=True).stdout
    res=[]; cur=None
    for line in out.splitlines():
        if line.startswith("@@"):
            if cur: res.append(cur)
            h, s = line[2:].split(" ",1) if " " in line[2:] else (line[2:],"")
            cur={"sha":h,"subject":s,"files":[]}
        elif line.strip() and cur: cur["files"].append(line.strip())
    if cur: res.append(cur)
    keep=[]
    for c in res:
        if not fixre.search(c["subject"]) or excl.search(c["subject"]): continue
        src=[f for f in c["files"] if f.endswith(".go") and not f.endswith("_test.go") and "/testdata/" not in f and not f.startswith("example")]
        if 0 < len(src) <= 5: keep.append(c)
    return keep
big = sys.argv[1]; small = sys.argv[2]
sample=[]
bigrepos=sorted(os.listdir(big))
allbig=[(r,c) for r in bigrepos for c in pool(os.path.join(big,r))]
print("big pool", len(allbig))
# 25 from popular repos, at most 5 per repo
R.shuffle(allbig); per={}
for r,c in allbig:
    if per.get(r,0)<5 and len([x for x in sample if x[0] in bigrepos])<25:
        per[r]=per.get(r,0)+1; sample.append((r,c))
smallrepos=[r for r in sorted(os.listdir(small)) if os.path.isdir(os.path.join(small,r,".git"))]
R.shuffle(smallrepos); n=0
for r in smallrepos:
    p=pool(os.path.join(small,r))
    if not p: continue
    sample.append((r,R.choice(p))); n+=1
    if n==25: break
json.dump([{"repo":r,"sha":c["sha"],"subject":c["subject"]} for r,c in sample], open("sample50.json","w"), indent=1)
for i,(r,c) in enumerate(sample,1): print(i, r, c["sha"][:10], c["subject"][:90])
