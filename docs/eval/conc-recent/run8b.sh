#!/bin/bash
# Evaluation 7, second amendment: both arms on the same concurrent harness
# (harness/<id>/go2lean_conc_test.go) at the case's parent. B fuzzes the
# properties A recorded, so both share one workspace.
#   A: go2lean run -deps -specs-only -budget $BUDGET
#   B: go2lean fuzz -fuzztime $FUZZTIME, built with -race
# Results in $OUT/<id>/. usage: run8b.sh [ids...]
# Reruns of cases the first pass lost to the harness or the machine, with
# the per-case adjustments below (recorded as deviations).
H=${H:?set H to the bench directory}
G=$H/tool/go2lean
BUDGET=${BUDGET:-300}; FUZZTIME=${FUZZTIME:-10m}; OUT=${OUT:-$H/results8}
export PATH=$HOME/.elan/bin:$PATH
ids=("$@")
[ ${#ids[@]} -eq 0 ] && ids=($(ls $H/harness | grep '^k'))
for id in "${ids[@]}"; do
  [ -f $H/harness/$id/go2lean_conc_test.go ] || { echo "$id no-harness" | tee -a $OUT/run8.log; continue; }
  read -r repo parent moddir pkgdir <<< "$(python3 -c "
import json
for l in open('$H/cases.jsonl'):
    c=json.loads(l)
    if c['id']=='$id': print(c['repo_dir'], c['parent'], c['module_dir'] or '.', c['package_dir'] or '.')")"
  out=$OUT/$id; wt=$H/w/$id; wsA=$H/ws/$id-A; wsB=$H/ws/$id-B
  rm -rf "$out" "$wsA" "$wsB"; mkdir -p "$out"
  git -C "$repo" worktree remove --force "$wt" 2>/dev/null
  git -C "$repo" worktree add -q --detach "$wt" "$parent" || { echo "$id worktree-failed" | tee -a $OUT/run8.log; continue; }
  cp $H/harness/$id/go2lean_conc_test.go "$wt/$pkgdir/"
  pre=""; aflags=""; tc=""
  case $id in
    k043) pre="go get github.com/nats-io/nats-server/v2@v2.15.2"; tc=auto ;;  # as the harness was written
    k044) aflags="-fuzz-specs=false" ;;  # #64: the package's own fuzz targets break loading
  esac
  mod=$wt/$moddir; rel=$(realpath --relative-to="$mod" "$wt/$pkgdir")
  gf=-mod=mod; [ -f "$wt/go.work" ] && gf=
  # Modules the screening needed (goleak) are fetched by -mod=mod.
  [ -n "$tc" ] && export GOTOOLCHAIN=$tc || unset GOTOOLCHAIN
  [ -n "$pre" ] && (cd "$mod" && GOFLAGS=$gf $pre) > "$out/pre.log" 2>&1
  s=$(date +%s)
  (cd "$mod" && GOFLAGS=$gf timeout 2400 $G run $aflags -deps -specs-only -budget $BUDGET -workspace "$wsA" -module . ./$rel) > "$out/A.log" 2>&1
  echo "rc=$? secs=$(( $(date +%s)-s ))" > "$out/A.time"
  mkdir -p "$out/wsA" && cp -r "$wsA"/{counterexamples,verdicts,hunts,proofs,properties,targets,poc} "$out/wsA/" 2>/dev/null
  (cd "$wsA" && find models -name '*.json' -exec cp --parents {} "$out/wsA/" \;) 2>/dev/null
  s=$(date +%s)
  (cd "$mod" && export GOFLAGS="$gf -race" && timeout 2400 $G fuzz -fuzztime $FUZZTIME -workspace "$wsA" -module . "$(GOFLAGS=$gf go list ./$rel)") > "$out/B.log" 2>&1
  echo "rc=$? secs=$(( $(date +%s)-s ))" > "$out/B.time"
  mkdir -p "$out/wsB" && cp -r "$wsA"/fuzz/*.json "$wsA"/fuzz/*.go "$wsA"/fuzz/testdata "$out/wsB/" 2>/dev/null
  rm -rf "$wsA"
  git -C "$repo" worktree remove --force "$wt"
  echo "$id A:$(cat $out/A.time) B:$(cat $out/B.time)" | tee -a $OUT/run8.log
done
echo done | tee -a $OUT/run8.log
