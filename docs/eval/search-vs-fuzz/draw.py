# Draw the candidate pool from mine.py's output: network clients and
# servers left out, at most 3 commits per library, in a seeded order.
# usage: python3 -I draw.py < candidates.jsonl > pool.jsonl
import collections, json, random, sys
R = random.Random(20261028)
io = {"go-redis_redis", "valyala_fasthttp", "prometheus_client_golang"}
by = collections.defaultdict(list)
for l in sys.stdin:
    c = json.loads(l)
    if c["repo"] not in io:
        by[c["repo"]].append(c)
pick = []
for repo in sorted(by):
    cs = by[repo]; R.shuffle(cs); pick += cs[:3]
R.shuffle(pick)
for i, c in enumerate(pick, 1):
    c["id"] = f"c{i:03d}"
    print(json.dumps(c))
