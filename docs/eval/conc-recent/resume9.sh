#!/bin/bash
# Resumes the evaluation 7 rerun after a container restart: runs arm A on
# the cases without an A.time (a case cut off mid-run is redone), then
# scores all of them. Detached: setsid nohup ./resume9.sh &
H=/tmp/claude-0/-home-user/2603d40b-be47-5ac1-abd7-f2d549bc2e2a/scratchpad/bench7
cd $H
ids=""
for id in $(ls harness | grep '^k'); do
  [ -f results9/$id/A.time ] || ids="$ids $id"
done
echo "resume $(date -u +%H:%M):$ids" >> results9/TOOL
[ -n "$ids" ] && ./run9.sh $ids >> results9/driver.log 2>&1
./score9.sh results9 > results9/score-driver.log 2>&1
echo ALLDONE >> results9/driver.log
