package semantics

import "sync"

// go2lean's own tests (#62): defer statements in loops run last first,
// with the arguments evaluated at the defer, interleaved with the other
// defers of the function.

type deferLog struct{ s []uint64 }

func (l *deferLog) add(x uint64) { l.s = append(l.s, x) }

func deferOrder(l *deferLog) {
	defer l.add(100)
	for i := uint64(0); i < 3; i++ {
		defer l.add(i)
		defer l.add(10 + i)
	}
	defer l.add(200)
}

func testDeferLoopOrder() bool {
	l := &deferLog{}
	deferOrder(l)
	want := []uint64{200, 12, 2, 11, 1, 10, 0, 100}
	if len(l.s) != len(want) {
		return false
	}
	for i := range want {
		if l.s[i] != want[i] {
			return false
		}
	}
	return true
}

func deferEarlyReturn(l *deferLog, n uint64) uint64 {
	for i := uint64(0); ; i++ {
		if i == n {
			return i
		}
		defer l.add(i)
	}
}

func testDeferLoopEarlyReturn() bool {
	l := &deferLog{}
	r := deferEarlyReturn(l, 2)
	return r == 2 && len(l.s) == 2 && l.s[0] == 1 && l.s[1] == 0
}

func testDeferLoopNone() bool {
	l := &deferLog{}
	return deferEarlyReturn(l, 0) == 0 && len(l.s) == 0
}

func deferUnlockAll(ms []*sync.Mutex) {
	for _, m := range ms {
		m.Lock()
		defer m.Unlock()
	}
}

func testDeferLoopUnlock() bool {
	ms := []*sync.Mutex{new(sync.Mutex), new(sync.Mutex)}
	deferUnlockAll(ms)
	return ms[0].TryLock() && ms[1].TryLock()
}
