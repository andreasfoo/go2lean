#!/bin/bash
# Scores each arm's failing input against the fix commit: the spec must
# hold there (then the input shows the defect the fix removed). Reads only
# the fix commit from truth.jsonl and prints nothing else from it.
# usage: score.sh <results dir> [ids...]
B=/tmp/claude-0/-home-user/2603d40b-be47-5ac1-abd7-f2d549bc2e2a/scratchpad/bench
G=$B/tool/go2lean
export PATH=$HOME/.elan/bin:$PATH
R=$(realpath "$1"); shift
ids=("$@")
[ ${#ids[@]} -eq 0 ] && ids=($(ls $R))
for id in "${ids[@]}"; do
  [ -d "$R/$id" ] || continue
  read -r repo pkgdir <<< "$(python3 -c "
import json
for l in open('$B/cases.jsonl'):
    c=json.loads(l)
    if c['id']=='$id': print(c['repo_dir'], c['package_dir'] or '.')")"
  fix=$(python3 -c "
import json
for l in open('$B/truth.jsonl'):
    t=json.loads(l)
    if t['id']=='$id': print(t['fix_sha'])")
  wt=$B/fixwt/$id
  [ -d "$wt" ] || git -C "$repo" worktree add -q --detach "$wt" "$fix" 2>/dev/null
  mod=$wt/$pkgdir
  while [ "$mod" != "$wt" ] && [ ! -f "$mod/go.mod" ]; do mod=$(dirname "$mod"); done
  gf="-mod=mod -vet=off"
  if [ ! -f "$mod/go.mod" ]; then
    mod=$wt
    (cd $wt && go mod init "$(python3 -c "
import json
for l in open('$B/cases.jsonl'):
    c=json.loads(l)
    if c['id']=='$id': print(c['module'])")" >/dev/null 2>&1 && GOFLAGS=-mod=mod go mod tidy >/dev/null 2>&1)
  fi
  [ -f "$wt/go.work" ] && gf="-vet=off"
  cp "$B/specs/$id/go2lean_spec_test.go" "$wt/$pkgdir/"
  # Arm A: replay its counterexample of the spec property on the fix.
  a="-"
  for v in "$R/$id"/ws/verdicts/*.json; do
    [ -f "$v" ] || continue
    read -r cx status kind <<< "$(python3 -c "
import json
v=json.load(open('$v'))
cx=v['counterexample']
c=json.load(open('$R/$id/ws/counterexamples/'+cx+'.json'))
print(cx, v['status'], 'tmpl' if c['property'].endswith('_nopanic') else 'spec')")"
    [ "$kind" = spec ] || continue
    if [ "$status" != confirmed ]; then a="not-confirmed($status)"; continue; fi
    w=$B/scorews/$id; rm -rf "$w"; mkdir -p "$w"; cp -r "$R/$id/ws/"* "$w/"; rm -rf "$w/verdicts"
    (cd "$mod" && GOFLAGS="$gf" timeout 600 $G poc -workspace "$w" -module . -counterexample "$cx") > "$R/$id/score-A.log" 2>&1
    st=$(python3 -c "
import json,glob
for f in glob.glob('$w/verdicts/*.json'): v=json.load(open(f)); print(v['status'], (v.get('diagnosis') or {}).get('reason',''))" 2>/dev/null)
    case "$st" in
      spurious*oracle_holds*) a="FOUND" ;;
      confirmed*) a="spec-fails-on-fix" ;;
      *) a="replay-error($st)" ;;
    esac
  done
  # Arm B: the fuzzer's failing inputs, on the fix.
  b="-"
  h=$(ls "$R/$id"/zz_*fuzz_test.go 2>/dev/null | head -1)
  for d in "$R/$id"/fuzz-testdata/fuzz/*/; do
    [ -d "$d" ] && [ -n "$h" ] || continue
    name=$(basename "$d")
    mkdir -p "$wt/$pkgdir/testdata/fuzz/$name"; cp "$d"* "$wt/$pkgdir/testdata/fuzz/$name/"; cp "$h" "$wt/$pkgdir/"
    for f in "$d"*; do
      if (cd "$wt/$pkgdir" && GOFLAGS="$gf" timeout 300 go test -run="^$name/$(basename $f)\$" . > "$R/$id/score-B.log" 2>&1); then b="FOUND"; else
        grep -q "is false\|FAIL" "$R/$id/score-B.log" && [ "$b" != FOUND ] && b="spec-fails-on-fix"
        grep -q "build failed\|cannot\|undefined" "$R/$id/score-B.log" && [ "$b" = "-" ] && b="build-error"
      fi
    done
  done
  echo "$id A:$a B:$b" | tee -a $R.score
done
