import os, re, subprocess, sys, collections
root = sys.argv[1]
cats = [
 ("nil pointer / nil map", r"\bnil\b|nil pointer|nil deref|nil map|npe\b"),
 ("index/slice bounds, off-by-one", r"out of range|out of bounds|index|off[- ]by[- ]one|bounds|slice"),
 ("panic (other/unspecified)", r"\bpanic"),
 ("integer overflow/conversion", r"overflow|underflow|int32|int64|uint|truncat|conversion"),
 ("unicode/utf8/string encoding", r"utf-?8|unicode|rune|multibyte|encoding|escap|quot"),
 ("parsing/format edge case", r"pars|format|empty string|empty input|whitespace|trailing|leading"),
 ("error handling", r"\berr(or)?s?\b.*(ignor|swallow|check|handl|wrap|return)|unchecked|handle error"),
 ("concurrency: race/deadlock/leak", r"\brace\b|deadlock|goroutine|leak|mutex|lock|channel|concurren|waitgroup"),
 ("resource: close/defer/timeout/context", r"\bclose\b|defer|timeout|context|cancel|file descriptor|\bfd\b"),
 ("performance/DoS/alloc", r"alloc|memory|perf|slow|quadratic|infinite loop|hang|oom|cpu"),
 ("time/date", r"\btime\b|timezone|date|duration|clock"),
 ("logic/wrong result", r"wrong|incorrect|should|instead of|mismatch|regression|typo"),
]
excl = re.compile(r"^(merge|bump|chore|docs?|ci|test|readme|release|update dep|go mod)", re.I)
fixre = re.compile(r"\b(fix|fixes|fixed|bug|crash|panic|correct)\b", re.I)
tot = 0; cnt = collections.Counter(); first = collections.Counter(); repos = 0
for r in sorted(os.listdir(root)):
    p = os.path.join(root, r)
    if not os.path.isdir(os.path.join(p, ".git")): continue
    repos += 1
    out = subprocess.run(["git", "-C", p, "log", "--no-merges", "--format=%s"], capture_output=True, text=True).stdout
    for s in out.splitlines():
        if excl.match(s) or not fixre.search(s): continue
        tot += 1
        low = s.lower()
        hits = [k for k, rx in cats if re.search(rx, low)]
        for k in hits: cnt[k] += 1
        first[hits[0] if hits else "unclassified"] += 1
print(f"repos {repos}, fix commits {tot}")
for k, v in first.most_common(): print(f"{k:42s} {v:5d} {100*v/tot:5.1f}%")
