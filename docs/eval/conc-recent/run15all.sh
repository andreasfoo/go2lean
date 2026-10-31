#!/bin/bash
# Reruns evaluation 7's fuel cases (k009 k016 k042) with tool-f627 (#100,
# livelock detection) into results15, trimming Go's build cache meanwhile,
# then scores them.
H=/tmp/claude-0/-home-user/2603d40b-be47-5ac1-abd7-f2d549bc2e2a/scratchpad/bench7
cd $H || exit 1
rm -f results15/finished
( while [ ! -f $H/results15/finished ]; do
    a=$(df --output=avail -BM / | tail -1 | tr -dc 0-9)
    [ "$a" -lt 2500 ] && find /root/.cache/go-build -type f -mmin +75 -delete 2>/dev/null
    [ "$a" -lt 800 ] && go clean -cache 2>/dev/null
    sleep 60
  done ) &
for r in repos/*; do git -C $r worktree prune 2>/dev/null; done
./run15.sh k009 k016 k042 > results15/driver.log 2>&1
G=$H/tool-f627/go2lean ./score9.sh results15 > results15/score-driver.log 2>&1
touch results15/finished
cat results15/run8.log results15/score8.log
for k in k009 k016 k042; do echo "== $k"; grep "_sequence random:" results15/$k/A.log | cut -c1-400; done
