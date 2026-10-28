# Candidate wrong-result fixes: a fix-like subject, 1-3 non-test Go files
# changed (no generated or vendored code), and a _test.go change in the
# same commit (a regression test: ground truth that the bug is reachable).
# usage: python3 -I mine.py <repos dir>... > candidates.jsonl
import json, os, re, subprocess, sys
fix = re.compile(r"\b(fix(e[sd])?|bug|wrong|incorrect(ly)?|broken|should|regression|edge case|off[- ]by[- ]one)\b", re.I)
skip = re.compile(r"\b(typo|lint|doc(s|umentation)?|readme|ci|workflow|build|deps?|dependenc|go\.mod|bump|release|version bump|race|data race|deadlock|leak|perf(ormance)?|speed|benchmark|alloc|windows|darwin|arm|386|wasm|flak|test(s)? only|comment|format(ting)?|vet|deprecat)\b", re.I)
for root in sys.argv[1:]:
    for repo in sorted(os.listdir(root)):
        path = os.path.join(root, repo)
        out = subprocess.run(["git", "-C", path, "log", "--no-merges", "--format=@@%H %ad %s", "--date=short", "--name-only", "HEAD"],
                             capture_output=True, text=True).stdout
        cur = None
        def emit(c):
            if not c: return
            sha, date, subj, files = c
            src = [f for f in files if f.endswith(".go") and not f.endswith("_test.go") and "/vendor/" not in f and "testdata/" not in f and not re.search(r"(_gen|\.pb|generated|tables?)\.go$", f)]
            tests = [f for f in files if f.endswith("_test.go")]
            if fix.search(subj) and not skip.search(subj) and 1 <= len(src) <= 3 and tests:
                print(json.dumps({"repo": repo, "sha": sha, "date": date, "subject": subj, "src": src, "tests": tests}))
        for line in out.splitlines():
            if line.startswith("@@"):
                emit(cur)
                sha, date, subj = line[2:].split(" ", 2)
                cur = [sha, date, subj, []]
            elif line.strip() and cur:
                cur[3].append(line.strip())
        emit(cur)
