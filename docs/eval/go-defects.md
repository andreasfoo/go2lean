# What defects Go code has

A survey to decide what go2lean should look for (issue #37). Three sources:
security advisories, published studies, and a sample of bug-fix commits.

## Security advisories (OSV, downloaded 2026-10-28)

Counted by `go-defects/family.py` (by CWE) and `go-defects/govulndb.py` (by
keywords), on the OSV exports for Go.

**GHSA, 4,893 Go advisories, grouped by CWE:**

| family | share |
|---|---|
| authorization and authentication | 28% |
| injection, SSRF, open redirect | 17% |
| path traversal and file handling | 12% |
| resource exhaustion (DoS) | 10% |
| information exposure | 8% |
| validation and logic | 7% |
| panic and memory | 6% |
| crypto, TLS and signatures | 6% |
| races | 2% |

**Go vulndb, the 286 entries for stdlib and golang.org/x** (closest to library
code), by keywords in the summary:

| family | share |
|---|---|
| resource exhaustion (quadratic work, unbounded allocation, missing recursion limit, infinite loop) | 26% |
| panic on crafted input | 22% |
| parser differential, escaping, request smuggling | 17% |
| authorization and authentication | 12% |
| crypto and verification | 10% |

OSS-Fuzz records in OSV are memory-safety bugs in C and C++; the panics,
OOMs and timeouts OSS-Fuzz reports for Go projects are not in OSV.

## Studies

- **Concurrency bugs (Tu et al., ASPLOS'19).** 171 bugs from six large
  projects.
  - About half are blocking bugs, half non-blocking.
  - Message passing causes as many bugs as shared memory.
  - More than half are Go-specific, such as closures capturing variables
    and misusing `WaitGroup`.
- **Data races at Uber (PLDI'22).** Over 2,000 races in 46M lines.
  - Variables captured by closures: the loop variable, `err`, named
    results.
  - Maps and slices accessed concurrently.
  - Mutexes copied by value.
- **Nil panics.** Uber built NilAway because nil panics caused production
  incidents.
- **Non-concurrency bugs.** We found no distribution for Go in the
  literature.

## Sample of 50 bug-fix commits

**How the sample was drawn** (`go-defects/sample50.json`, `go-defects/sample.py`,
seed 20261028):
- 25 commits from gin, cobra, chi, BurntSushi/toml, go-redis and the
  Prometheus client library.
- 25 commits from 25 small libraries.
- Each commit has "fix", "bug", "crash" or "panic" in its subject, changes 1
  to 5 non-test Go files, and has no docs, test or lint keyword in its
  subject.

Each diff was read and classified once, by one reader
(`go-defects/sample50-classified.tsv`).

**What the commits fix.** 9 commits are not defects (typos, lint, docs, a
syntax error). The 41 defects:

| symptom | count |
|---|---|
| wrong result (no crash) | 28 (68%) |
| panic | 4 (10%) |
| concurrency (race, pool starvation) | 4 (10%) |
| hang | 1 |
| nondeterminism (map mutated while ranged over) | 1 |
| leak, performance, platform build | 3 |

**What could find them:**

| oracle | defects |
|---|---|
| a domain spec or a reference | 25 (61%): about 12 described in docs or comments, about 8 with a reference (CommonMark tests, Unicode test data, the C++ implementation, a published formula) |
| no panic from an entry point on nil, empty or boundary inputs | 4 |
| concurrency checks | 4 |
| bytes against runes on multibyte input | 2 |
| generic invariants (range, reflexivity, round trip) | 1 to 3 |
| termination or resource amplification | 1 to 2 |
| determinism under map order | 1 |
| linter | 1 |
| side effects (writes to stdout, not the given writer) | 1 |

**Caveats:** a small sample, one reader, a keyword-based draw, and small
libraries partly chosen earlier for panic fixes. Read the shares as orders
of magnitude.

## Conclusion

- **What Go defects look like.** Most are wrong results, not panics.
  Generic properties that need no spec reach at most about a quarter of
  them.
- **Where the rest are found.** It takes an oracle: what the docs promise,
  or what a reference implementation does.
- **What go2lean should do.** Its main path is blind specs from
  documentation and differential checks against references. Generic
  built-ins complement them.
