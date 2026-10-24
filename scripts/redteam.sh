#!/bin/bash
# The proof judge's red-team corpus (testdata/redteam): each attack claims
# something false of a function and tries to get go2lean prove to accept
# it (an axiom, sorry under another name, native_decide, an instance or
# notation hijack, the kernel skipped, files written at elaboration, ...).
# Every attack must be rejected, the control goal (true, with a real
# proof) accepted, and no file outside the sandboxes changed. Every attack
# goes to the judge (-judge-all), also one Lean's check in the workspace
# already rejected: the judge alone must reject it.
# usage: scripts/redteam.sh <workspace> [jobs]
set -e
cd "$(dirname "$0")/.."
ROOT=$(pwd)
WS=${1:?workspace}
JOBS=${2:-4}
rm -rf "$WS"; mkdir -p "$WS"
rm -f /tmp/go2lean-redteam-pwned
go build -o "$WS/g2l" ./cmd/go2lean
export GO2LEAN_REDTEAM_ROOT=$ROOT
before=$(git status --porcelain --untracked-files=all lean)
# One run first, alone: it builds the judge's core for the others.
if ! "$WS/g2l" prove -workspace "$WS/control" -goals testdata/redteam/control.json ./testdata/redteam > "$WS/control.log" 2>&1; then
  tail -20 "$WS/control.log"; echo "redteam: the control goal was not proved"; exit 1
fi
grep '^  proved' "$WS/control.log"
run() {
  name=$(basename "$1" .json)
  if "$WS/g2l" prove -workspace "$WS/$name" -paranoid=false -judge-all -goals "$1" ./testdata/redteam > "$WS/$name.log" 2>&1; then
    echo "ACCEPTED $name"
  elif ! grep -q "^  open .*($name).*the judge rejected it" "$WS/$name.log"; then
    echo "ERROR    $name: not judged: $(grep "($name)" "$WS/$name.log" | tail -1 | cut -c1-160)"
  else
    echo "rejected $name: $(grep "^  open .*($name)" "$WS/$name.log" | sed 's|: see /.*||; s|^  open  *||' | cut -c1-200)"
  fi
}
export -f run; export WS
ls testdata/redteam/attacks/*.json | xargs -P "$JOBS" -I{} bash -c 'run {}' | sort > "$WS/verdicts.txt"
cat "$WS/verdicts.txt"
# Whole solution files, which may import Lean's metaprogramming (an
# answer cannot): each must elaborate (the attack works in Lean) and be
# rejected by the comparator, against the challenge of a false goal.
# The judge's project of an attack that got that far (its goal is false).
J=$(ls -d "$WS"/shadow_name/judge/*/ | head -1)
for f in testdata/redteam/solutions/*.json; do
  name=solution-$(basename "$f" .json)
  D="$WS/$name"; rm -rf "$D"; cp -a "$J" "$D"
  python3 -I testdata/redteam/solution.py "$D/Challenge.lean" "$f" > "$D/Solution.lean"
  elab=$(cd "$D" && lake env lean Solution.lean 2>&1 | grep -c 'error' || true)
  if (cd "$D" && lake comparator > "$D/judge.log" 2>&1); then
    echo "ACCEPTED $name" >> "$WS/verdicts.txt"
  elif [ "$elab" != 0 ]; then
    echo "ERROR    $name: the attack does not elaborate" >> "$WS/verdicts.txt"
  else
    echo "rejected $name: elaborates; $(grep -i 'rejected\|error' "$D/judge.log" | head -1 | cut -c1-120)" >> "$WS/verdicts.txt"
  fi
done
grep '^.* solution-' "$WS/verdicts.txt"
fail=0
if grep -q '^ACCEPTED\|^ERROR' "$WS/verdicts.txt"; then
  echo "redteam: an attack was accepted, or a run failed"; fail=1
fi
after=$(git status --porcelain --untracked-files=all lean)
if [ "$before" != "$after" ] || [ -e /tmp/go2lean-redteam-pwned ] || ls "${GO2LEAN_CACHE:-$HOME/.cache/go2lean}"/pwned* >/dev/null 2>&1 \
   || ls "$WS"/*/lean/Go2Lean/Generated/gossa/pwned* >/dev/null 2>&1; then
  echo "redteam: an attack wrote a file outside the sandbox"; git status --porcelain lean; fail=1
fi
[ $fail = 0 ] && echo "redteam: $(grep -c '^rejected' "$WS/verdicts.txt") attacks rejected, the control proved"
exit $fail
