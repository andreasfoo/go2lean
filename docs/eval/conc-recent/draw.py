# Draw the pool from mine.py's output: at most 5 commits per project, in a
# seeded order. usage: python3 -I draw.py < candidates.jsonl > pool.jsonl
import collections, json, random, sys
R = random.Random(20261028)
by = collections.defaultdict(list)
for l in sys.stdin:
    c = json.loads(l)
    by[c["repo"]].append(c)
pick = []
for repo in sorted(by):
    cs = by[repo]; R.shuffle(cs); pick += cs[:5]
R.shuffle(pick)
for i, c in enumerate(pick, 1):
    c["id"] = f"k{i:03d}"
    print(json.dumps(c))
