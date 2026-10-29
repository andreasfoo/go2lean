#!/bin/bash
# Scores each arm's failing input of the harness at the fix commit. A: the
# counterexample replayed there with go2lean poc (repeated -race runs and
# the model's schedule); B: the fuzzer's failing input run 20 times under
# -race. A find counts when the input does not fail at the fix; its kind
# is then checked by reading the parent's report.
# usage: score8.sh <results dir> [ids...]
H=${H:?set H to the bench directory}
G=$H/tool/go2lean
export PATH=$HOME/.elan/bin:$PATH
R=$(realpath "$1"); shift
ids=("$@")
[ ${#ids[@]} -eq 0 ] && ids=($(ls $R | grep '^k'))
for id in "${ids[@]}"; do
  [ -d "$R/$id" ] || continue
  read -r repo fix moddir pkgdir <<< "$(python3 -c "
import json
for l in open('$H/cases.jsonl'):
    c=json.loads(l)
    if c['id']=='$id': print(c['repo_dir'], c['fix'], c['module_dir'] or '.', c['package_dir'] or '.')")"
  wt=$H/fw/$id
  git -C "$repo" worktree remove --force "$wt" 2>/dev/null
  git -C "$repo" worktree add -q --detach "$wt" "$fix" || { echo "$id fix-worktree-failed" | tee -a $R/score8.log; continue; }
  h=$H/harness/$id/go2lean_conc_test.go
  [ -f $H/harness/$id/go2lean_conc_test.fix.go.txt ] && h=$H/harness/$id/go2lean_conc_test.fix.go.txt
  cp "$h" "$wt/$pkgdir/go2lean_conc_test.go"
  mod=$wt/$moddir
  gf=-mod=mod; [ -f "$wt/go.work" ] && gf=
  # A: replay each confirmed counterexample of the harness at the fix.
  a="-"
  for v in "$R/$id"/wsA/verdicts/*.json; do
    [ -f "$v" ] || continue
    read -r cx status <<< "$(python3 -c "
import json; v=json.load(open('$v')); print(v['counterexample'], v['status'])")"
    if [ "$status" != confirmed ]; then [ "$a" = "-" ] && a="not-confirmed($status)"; continue; fi
    w=$H/scorews/$id; rm -rf "$w"; mkdir -p "$w"; cp -r "$R/$id/wsA/"* "$w/"; rm -rf "$w/verdicts" "$w/poc"
    (cd "$mod" && GOFLAGS="$gf" timeout 900 $G poc -workspace "$w" -module . -counterexample "$cx") > "$R/$id/score-A.log" 2>&1
    st=$(python3 -c "
import json,glob
for f in glob.glob('$w/verdicts/*.json'): v=json.load(open(f)); print(v['status'], (v.get('diagnosis') or {}).get('reason',''))" 2>/dev/null)
    case "$st" in
      spurious*|*race_not_observed*) a="FOUND" ;;
      confirmed*) a="fails-on-fix" ;;
      *) a="replay-error($st)" ;;
    esac
    rm -rf "$w"
  done
  # B: the fuzzer's failing input, 20 times under -race at the fix.
  b="-"
  zf=$(ls "$R/$id"/wsB/zz_*fuzz_test.go 2>/dev/null | head -1)
  for d in "$R/$id"/wsB/testdata/fuzz/*/; do
    [ -d "$d" ] && [ -n "$zf" ] || continue
    name=$(basename "$d")
    mkdir -p "$wt/$pkgdir/testdata/fuzz/$name"; cp "$d"* "$wt/$pkgdir/testdata/fuzz/$name/"; cp "$zf" "$wt/$pkgdir/"
    for f in "$d"*; do
      if (cd "$wt/$pkgdir" && GOFLAGS="$gf -vet=off" timeout 900 go test -race -count=20 -timeout 600s -run="^$name/$(basename $f)\$" . > "$R/$id/score-B.log" 2>&1); then b="FOUND"
      elif grep -q "build failed\|undefined:\|cannot use" "$R/$id/score-B.log"; then b="build-error"
      else b="fails-on-fix"; fi
    done
  done
  git -C "$repo" worktree remove --force "$wt"
  echo "$id A:$a B:$b" | tee -a $R/score8.log
done
