# Writing a concurrency harness for one evaluation-7 case

Each case is a real concurrency bug fixed in an open-source Go project
(bench7/cases.jsonl: repo_dir, parent, fix, module_dir, package_dir, kind,
notes). bench7/reach-out/<id>.changed lists the functions the fix changes
(import path, receiver type, name); bench7/reach-out/<id>.txt lists the
package's exported functions/methods and tests that reach them in the call
graph, nearest first.

The harness is the ONE program both arms of the evaluation search: go2lean
explores its inputs and goroutine schedules in a model; `go test -fuzz
-race` explores its inputs and leaves schedules to Go's scheduler. It must
therefore be a fair, natural concurrent use of the API, not a forced
reproduction.

## The harness

A file `go2lean_conc_test.go` in the package directory (package clause as
the package's internal tests), at the PARENT commit, holding exactly one
function:

```go
// go2lean:sequence <Target>
// SeqConc<Short>: <one line: which concurrent use, and why the package
// should allow it: quote the doc comment or the fix's description>.
func SeqConc<Short>(<inputs>) bool {
	// build ONE shared object (exported constructor if there is one;
	// otherwise the smallest setup the package's own tests use)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); /* first call(s) */ }()
	go func() { defer wg.Done(); /* second call(s) */ }()
	wg.Wait()
	return <true, or a check of the result for a wrong_result case>
}
```

- `<Target>` is the function or method (as `Type.Method`) of the package
  the harness drives most directly.
- Inputs: only ints, uints, bools, strings, []byte (and slices of those).
  The searches generate them; use them as the calls' arguments or to choose
  among a few calls (e.g. `op % 3`). Do not use structs, maps, funcs,
  interfaces or channels as parameters.
- Two goroutines (three only if the bug needs three parties). Each makes
  one or a few calls. Every goroutine must end: no infinite loops, no
  waits on things nobody will signal in a correct program.
- NO sleeps, no `runtime.Gosched`, no hooks, no copying of the fix, nothing
  that forces an interleaving: the searches must find it.
- No network, files, or real time if the bug can be reached without them.
  If it cannot (loopback servers, timers), write the harness anyway and
  record `uses_net` / `uses_time` in meta.json.
- For a `wrong_result` kind, return false when the outcome is impossible
  under any sequential order of the two goroutines' calls (a linearizability
  check), or violates a documented invariant. Otherwise return true; races,
  deadlocks, leaks and panics are detected by the tools, not by the
  return value.
- It may call unexported functions (it is an internal test file), but
  prefer the API a user of the package would call concurrently.

## Checks (in a worktree at the parent, then at the fix)

1. It compiles: `go test -c -vet=off -race -o /dev/null ./<pkg>`
   (GOFLAGS=-mod=mod unless the repo has a go.work).
2. A quick sanity run, informational only: a throwaway
   `zz_drive_test.go` with `func TestDriveSeqConc(t *testing.T) { for
   i := 0; i < 200; i++ { if !SeqConc<Short>(<simple fixed inputs>) {
   t.Fatal("false") } } }`, run once with `-race -run TestDriveSeqConc
   -count=1 -timeout 120s` at the parent and at the fix (bring the harness
   to the fix worktree; adapt only if an API it calls changed). Record
   whether a race/deadlock/panic/false appeared at each. Delete the driver
   afterwards: it is not part of the harness.
3. At the fix commit, the harness must compile and the sanity run must be
   clean; if it is not, the harness is wrong (it reports something the fix
   did not change): rework it, or record the problem.

## Output

bench7/harness/<id>/go2lean_conc_test.go (the harness, as written for the
parent) and bench7/harness/<id>/meta.json:
{"id", "target", "seq": "SeqConc<Short>", "package_dir", "uses_net",
"uses_time", "parent_sanity" (e.g. "race 37/200", "clean 200/200",
"deadlock", "false 3/200"), "fix_sanity", "fix_adapted" (bool),
"notes" (one or two lines)}.

If a harness cannot be written (the bug needs a server process, or no
natural concurrent use exists), write meta.json with "skip": "<reason>".

## Hygiene

Worktrees under bench7/hw/<id>-parent and <id>-fix (`git worktree add
--detach`), at most two at a time, removed when done (`git worktree remove
--force`). Never run `go clean -cache` (other agents share the cache); if
free disk (`df -h /tmp`) falls under 1.5 GB, stop and say so. Never use
`pkill`/`killall` by pattern; kill only PIDs you started. The case's own
notes (cases.jsonl) say which modules needed `go get` in the screening.
