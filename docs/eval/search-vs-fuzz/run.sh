#!/bin/bash
# Evaluation 5: for each case, arm A (go2lean run) then arm B (go2lean fuzz
# on the same spec), one case at a time. Results in results/<id>/.
# usage: run.sh [ids...]
B=/tmp/claude-0/-home-user/2603d40b-be47-5ac1-abd7-f2d549bc2e2a/scratchpad/bench
G=$B/tool/go2lean
export PATH=$HOME/.elan/bin:$PATH
ids=("$@")
[ ${#ids[@]} -eq 0 ] && ids=($(python3 -c "import json;[print(json.loads(l)['id']) for l in open('$B/cases.jsonl')]"))
for id in "${ids[@]}"; do
  read -r pkgdir pkg <<< "$(python3 -c "
import json
for l in open('$B/cases.jsonl'):
    c=json.loads(l)
    if c['id']=='$id': print(c['package_dir'] or '.', c['package'])")"
  root=$B/cases/$id
  mod=$root/$pkgdir
  while [ "$mod" != "$root" ] && [ ! -f "$mod/go.mod" ]; do mod=$(dirname "$mod"); done
  rel=$(realpath --relative-to="$mod" "$root/$pkgdir")
  out=$B/results$SUFFIX/$id; ws=$B/ws/$id
  rm -rf "$out" "$ws"; mkdir -p "$out"
  cmp -s "$B/specs/$id/go2lean_spec_test.go" "$root/$pkgdir/go2lean_spec_test.go" || { echo "$id spec-changed" | tee -a $B/run.log; continue; }
  # A go.work at the root (a multi-module repository) forbids -mod=mod.
  gf=-mod=mod; [ -f "$root/go.work" ] && gf=
  # Reruns: VETOFF=1 turns off go test's vet (old test files fail Go 1.24's
  # vet, and with it the replay and the fuzzing build).
  [ "$VETOFF" = 1 ] && gf="$gf -vet=off"
  # The package's real import path, from the targets A recorded.
  real=$(python3 -c "
import json,glob,sys
for f in glob.glob('$B/results/$id/ws/targets/*.json'):
    print(json.load(open(f))['package']); break" 2>/dev/null)
  [ -n "$real" ] && [ "$real" != "$pkg" ] && grep -q "\"package\": \"$real\"" $B/results/$id/ws/targets/*.json && pkg=$real
  s=$(date +%s)
  (cd "$mod" && GOFLAGS=$gf timeout 1800 $G run -deps -budget 300 -prove=false -workspace "$ws" -module . ./$rel) > "$out/A.log" 2>&1
  echo "rc=$? secs=$(( $(date +%s)-s ))" > "$out/A.time"
  s=$(date +%s)
  (cd "$mod" && GOFLAGS=$gf timeout 900 $G fuzz -fuzztime 5m -workspace "$ws" -module . "$pkg") > "$out/B.log" 2>&1
  echo "rc=$? secs=$(( $(date +%s)-s ))" > "$out/B.time"
  # Keep the records, drop the Lean build.
  mkdir -p "$out/ws" && cp -r "$ws"/{counterexamples,verdicts,hunts,proofs,properties,targets,models} "$out/ws/" 2>/dev/null
  find "$out/ws/models" -name '*.lean' -delete 2>/dev/null
  cp -r "$ws/fuzz/testdata" "$out/fuzz-testdata" 2>/dev/null; cp "$ws/fuzz/zz_go2lean_fuzz_test.go" "$out/" 2>/dev/null
  rm -rf "$ws"
  (cd "$root" && git status --porcelain | grep -v go2lean_spec_test.go | grep -v '^?? go.sum$\|^?? go.mod$\|^ M go.mod$\|^ M go.sum$' > "$out/dirty.txt")
  echo "$id A:$(cat $out/A.time) B:$(cat $out/B.time)" | tee -a $B/run$SUFFIX.log
done
echo done | tee -a $B/run$SUFFIX.log
