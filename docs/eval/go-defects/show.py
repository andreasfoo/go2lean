import json, subprocess, sys, os
s = json.load(open("sample50.json")); a, b = int(sys.argv[1]), int(sys.argv[2])
# Clones under the directories given in REPOS (colon-separated), by repo name.
roots = os.environ.get("REPOS", "repos").split(":")
for i, e in enumerate(s[a-1:b], a):
    p = next((os.path.join(r, e["repo"]) for r in roots if os.path.isdir(os.path.join(r, e["repo"]))), e["repo"])
    d = subprocess.run(["git","-C",p,"show","--format=%B","-U2",e["sha"],"--",":(glob)**/*.go",":(exclude,glob)**/*_test.go",":(exclude,glob)**/testdata/**"],capture_output=True,text=True).stdout
    lines = [l for l in d.splitlines() if not l.startswith(("index ","diff --git","--- ","+++ "))]
    print(f"===== #{i} {e['repo']} {e['sha'][:10]}")
    print("\n".join(l[:150] for l in lines[:55]))
    if len(lines) > 55: print(f"... ({len(lines)-55} more lines)")
