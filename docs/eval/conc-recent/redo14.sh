#!/bin/bash
# Reruns the eval 7 arm A cases that results14 lost to a full disk, trimming
# Go's build cache while they run (as cachetrim9.sh), then scores results14.
H=/tmp/claude-0/-home-user/2603d40b-be47-5ac1-abd7-f2d549bc2e2a/scratchpad/bench7
cd $H
( while pgrep -f "run14.sh|score9.sh" >/dev/null || [ ! -f $H/results14/redo.done ]; do
    a=$(df --output=avail -BM / | tail -1 | tr -dc 0-9)
    [ "$a" -lt 2500 ] && find /root/.cache/go-build -type f -mmin +75 -delete 2>/dev/null
    [ "$a" -lt 800 ] && go clean -cache 2>/dev/null
    sleep 60
  done ) &
ids=""
for id in $(ls harness | grep '^k'); do
  [ -s results14/$id/A.time ] && ! grep -q "no space left\|unable to execute command: Bus error\|failed to create file" results14/$id/A.log || ids="$ids $id"
done
echo "redo $(date -u +%H:%M):$ids" >> results14/TOOL
[ -n "$ids" ] && ./run14.sh $ids >> results14/driver.log 2>&1
G=$H/tool-main/go2lean ./score9.sh results14 > results14/score-driver.log 2>&1
touch results14/redo.done
echo ALLDONE >> results14/driver.log
