# Candidate concurrency fixes since 2023: a subject naming a race, deadlock,
# leak, hang or a synchronisation primitive; 1-5 non-test Go files changed
# (no generated or vendored code); a _test.go change in the same commit.
# usage: python3 -I mine.py <repos dir> > candidates.jsonl
import json, os, re, subprocess, sys
conc = re.compile(r"\b(data ?race|races?|racy|deadlocks?|deadlocked|leak(s|ed|ing)?|goroutines?|hang(s|ing)?|hung|stuck|block(s|ed|ing)|concurren\w*|mutex|lock(s|ed|ing)?|unlock|waitgroup|channel|chan|close of closed|closed channel|sync\.\w+|atomic\w*|livelock)\b", re.I)
fix = re.compile(r"\b(fix(e[sd])?|bug|avoid|prevent|resolve[sd]?|correct)\b", re.I)
skip = re.compile(r"\b(flak(y|e|iness)|test(s)? only|ci|lint|docs?|typo|bump|deps?|dependabot|revert|benchmark|perf(ormance)?)\b", re.I)
root = sys.argv[1]
for repo in sorted(os.listdir(root)):
    path = os.path.join(root, repo)
    out = subprocess.run(["git", "-C", path, "log", "--no-merges", "--since=2023-01-01", "--format=@@%H %ad %s", "--date=short", "--name-only", "HEAD"],
                         capture_output=True, text=True).stdout
    cur = None
    def emit(c):
        if not c: return
        sha, date, subj, files = c
        src = [f for f in files if f.endswith(".go") and not f.endswith("_test.go") and "/vendor/" not in f and "testdata/" not in f and not re.search(r"(\.pb|_gen|generated)\.go$", f)]
        tests = [f for f in files if f.endswith("_test.go")]
        if conc.search(subj) and fix.search(subj) and not skip.search(subj) and 1 <= len(src) <= 5 and tests:
            print(json.dumps({"repo": repo, "repo_dir": os.path.abspath(path), "sha": sha, "date": date, "subject": subj, "src": src, "tests": tests}))
    for line in out.splitlines():
        if line.startswith("@@"):
            emit(cur)
            sha, date, subj = line[2:].split(" ", 2)
            cur = [sha, date, subj, []]
        elif line.strip() and cur:
            cur[3].append(line.strip())
    emit(cur)
