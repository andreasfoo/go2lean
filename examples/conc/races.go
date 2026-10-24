package conc

import (
	"sync"
	"sync/atomic"
)

// Data races (C2): two goroutines access the same variable, one of them
// writing, and nothing orders the accesses. As in conc.go, each function is
// either race-free under every schedule or has one bug, named in its
// comment.

// RacyCount counts two goroutines into a shared int. Bug: a data race on n
// (the increments are not ordered).
func RacyCount() int {
	n := 0
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n++
		}()
	}
	wg.Wait()
	return n
}

// LockedCount is RacyCount with a mutex. Race-free.
func LockedCount() int {
	n := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			n++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return n
}

// AtomicCount is RacyCount with an atomic counter. Race-free.
func AtomicCount() int64 {
	var n int64
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			atomic.AddInt64(&n, 1)
		}()
	}
	wg.Wait()
	return atomic.LoadInt64(&n)
}

// Publish hands a result over through a channel. Race-free: the send
// orders the write before the read.
func Publish(v int) int {
	res := 0
	done := make(chan bool)
	go func() {
		res = v * 2
		done <- true
	}()
	<-done
	return res
}

// PeekEarly reads the result when asked to before waiting. Bug: with
// early, a data race on res (the read is not ordered after the write).
func PeekEarly(v int, early bool) int {
	res := 0
	done := make(chan bool)
	go func() {
		res = v * 2
		close(done)
	}()
	if early && v > 100 {
		return res
	}
	<-done
	return res
}

// cache is a map guarded by a read-write mutex, except in getUnlocked.
type cache struct {
	mu sync.RWMutex
	m  map[string]int
}

func (c *cache) put(k string, v int) {
	c.mu.Lock()
	c.m[k] = v
	c.mu.Unlock()
}

func (c *cache) get(k string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[k]
}

func (c *cache) getUnlocked(k string) int { return c.m[k] }

// FillAndRead writes and reads the cache from two goroutines. Race-free.
func FillAndRead() int {
	c := &cache{m: map[string]int{}}
	done := make(chan bool)
	go func() {
		c.put("a", 1)
		done <- true
	}()
	r := c.get("a")
	<-done
	return r
}

// FillAndPeek is FillAndRead reading without the lock. Bug: a data race on
// the map.
func FillAndPeek() int {
	c := &cache{m: map[string]int{}}
	done := make(chan bool)
	go func() {
		c.put("a", 1)
		done <- true
	}()
	r := c.getUnlocked("a")
	<-done
	return r
}

// hits is a package-level counter.
var hits int

// CountHits counts two goroutines into hits. Bug: a data race on the
// package variable.
func CountHits() int {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits++
		}()
	}
	wg.Wait()
	return hits
}

// stats counts writes atomically.
type stats struct {
	writes uint64
}

// SnapshotRacy counts a write atomically in one goroutine and copies the
// stats plainly in another. Bug: a data race on writes (an atomic write and
// a plain read are not ordered).
func SnapshotRacy() uint64 {
	st := &stats{}
	done := make(chan bool)
	go func() {
		atomic.AddUint64(&st.writes, 1)
		done <- true
	}()
	snap := *st
	<-done
	return snap.writes
}

// SnapshotAtomic is SnapshotRacy reading atomically. Race-free.
func SnapshotAtomic() uint64 {
	st := &stats{}
	done := make(chan bool)
	go func() {
		atomic.AddUint64(&st.writes, 1)
		done <- true
	}()
	n := atomic.LoadUint64(&st.writes)
	<-done
	return n
}

// AddInside counts one worker into a WaitGroup from inside the worker.
// Bug: Add is concurrent with Wait (Wait may find the counter at zero and
// return before the worker ran, and go test -race reports the misuse).
func AddInside() int {
	var wg sync.WaitGroup
	n := int64(0)
	start := make(chan bool)
	go func() {
		<-start
		wg.Add(1)
		atomic.AddInt64(&n, 1)
		wg.Done()
	}()
	close(start)
	wg.Add(1)
	go func() { wg.Done() }()
	wg.Wait()
	return int(atomic.LoadInt64(&n))
}

// AddBefore is AddInside with every Add before the goroutines start.
// Race-free.
func AddBefore() int {
	var wg sync.WaitGroup
	n := int64(0)
	wg.Add(2)
	go func() {
		atomic.AddInt64(&n, 1)
		wg.Done()
	}()
	go func() { wg.Done() }()
	wg.Wait()
	return int(atomic.LoadInt64(&n))
}
