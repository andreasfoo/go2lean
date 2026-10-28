#!/bin/bash
# Arm B only, for a case whose A result stands: fuzz the spec in a copy of
# A's workspace records. usage: fuzzonly.sh <results dir> <id> <package>
B=/tmp/claude-0/-home-user/2603d40b-be47-5ac1-abd7-f2d549bc2e2a/scratchpad/bench
R=$B/$1; id=$2; pkg=$3
pkgdir=$(python3 -c "
import json
for l in open('$B/cases.jsonl'):
    c=json.loads(l)
    if c['id']=='$id': print(c['package_dir'] or '.')")
root=$B/cases/$id; mod=$root/$pkgdir
while [ "$mod" != "$root" ] && [ ! -f "$mod/go.mod" ]; do mod=$(dirname "$mod"); done
gf="-mod=mod -vet=off"; [ -f "$root/go.work" ] && gf="-vet=off"
ws=$B/ws/fz-$id; rm -rf "$ws"; mkdir -p "$ws"; cp -r "$R/$id/ws/"* "$ws/"
s=$(date +%s)
(cd "$mod" && GOFLAGS="$gf" timeout 900 $B/tool/go2lean fuzz -fuzztime 5m -workspace "$ws" -module . "$pkg") > "$R/$id/B.log" 2>&1
echo "rc=$? secs=$(( $(date +%s)-s ))" > "$R/$id/B.time"
rm -rf "$R/$id/fuzz-testdata"; cp -r "$ws/fuzz/testdata" "$R/$id/fuzz-testdata" 2>/dev/null; cp "$ws/fuzz/zz_go2lean_fuzz_test.go" "$R/$id/" 2>/dev/null
rm -rf "$ws"
echo "$id B:$(cat $R/$id/B.time)"
