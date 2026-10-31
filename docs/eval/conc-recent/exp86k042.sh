#!/bin/bash
# exp86k042.sh: k042's harness, random engine only (100 trials), with
# tool-hang: where its runs run out of fuel (#86).
H=/tmp/claude-0/-home-user/2603d40b-be47-5ac1-abd7-f2d549bc2e2a/scratchpad/bench7
export PATH=$HOME/.elan/bin:$PATH
repo=$H/repos/prometheus_prometheus; wt=$H/w/k042-x; ws=$H/ws/k042-x
git -C $repo worktree remove --force $wt 2>/dev/null; rm -rf $ws
git -C $repo worktree add -q --detach $wt b8d9a9b77af5501d5920014b8478b21f0e1b16fc
cp $H/harness/k042/go2lean_conc_test.go $wt/tsdb/
gf=-mod=mod; [ -f $wt/go.work ] && gf=
(cd $wt && GOFLAGS=$gf timeout 3000 $H/tool-hang/go2lean run -deps -specs-only -engine random -trials 100 -budget 0 -max-mutants 0 -workspace $ws -module . ./tsdb) > $H/exp86k042.log 2>&1
echo "rc=$?" >> $H/exp86k042.log
grep "_sequence random:" $H/exp86k042.log | cut -c1-700
git -C $repo worktree remove --force $wt; rm -rf $ws
