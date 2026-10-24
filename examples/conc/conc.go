// Package conc is the ground-truth package for concurrency in the model
// (docs/concurrency.md): goroutines, channels, select and the sync
// primitives. Each function either is correct under every schedule or has
// one bug, named in its comment.
package conc

import (
	"sync"
	"sync/atomic"
)

// Pipe hands n to a goroutine and back. Correct.
func Pipe(n int) int {
	ch := make(chan int)
	go func() { ch <- n * 2 }()
	return <-ch
}

// Notify sends n when it is large. Bug: an unbuffered send nobody
// receives, so Notify(11) deadlocks (a data-dependent blocking bug).
func Notify(n int) int {
	ch := make(chan int)
	if n > 10 {
		ch <- n
	}
	return n
}

// First waits for a worker unless told to give up. Bug: when it gives up,
// the worker stays blocked on its send forever (a goroutine leak).
func First(giveUp bool) int {
	ch := make(chan int)
	go func() { ch <- 1 }()
	if giveUp {
		return 0
	}
	return <-ch
}

// FirstBuffered is First with room for the worker's value. Correct.
func FirstBuffered(giveUp bool) int {
	ch := make(chan int, 1)
	go func() { ch <- 1 }()
	if giveUp {
		return 0
	}
	return <-ch
}

// Sum adds xs in parallel workers under a mutex. Correct.
func Sum(xs []int) int {
	var mu sync.Mutex
	var wg sync.WaitGroup
	total := 0
	for _, x := range xs {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			mu.Lock()
			total += v
			mu.Unlock()
		}(x)
	}
	wg.Wait()
	return total
}

type Counter struct {
	mu sync.Mutex
	n  int
}

func (c *Counter) inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

// AddMore adds k, and once more when k is large. Bug: the extra inc takes
// the mutex AddMore already holds (a self-deadlock for k > 3).
func AddMore(k int) int {
	c := &Counter{}
	c.mu.Lock()
	c.n += k
	if k > 3 {
		c.inc()
	}
	c.mu.Unlock()
	return c.n
}

// Drain closes the channel once, or twice when asked. Bug: the second
// close panics.
func Drain(again bool) int {
	ch := make(chan int, 2)
	ch <- 1
	close(ch)
	if again {
		close(ch)
	}
	n := 0
	for v := range ch {
		n += v
	}
	return n
}

// TrySend sends without blocking. Correct.
func TrySend(full bool) bool {
	ch := make(chan int, 1)
	if full {
		ch <- 1
	}
	select {
	case ch <- 2:
		return true
	default:
		return false
	}
}

type Store struct {
	mu sync.RWMutex
	v  int
}

func (s *Store) get() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.v
}

func (s *Store) sum() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.v + s.get()
}

// ReadWhileWriting reads twice under a read lock while a writer runs. Bug:
// when the writer asks for the lock between the two read locks, writer
// preference blocks the second one: a deadlock in some schedules only.
func ReadWhileWriting() int {
	s := &Store{}
	done := make(chan bool)
	go func() {
		s.mu.Lock()
		s.v = 1
		s.mu.Unlock()
		done <- true
	}()
	r := s.sum()
	<-done
	return r
}

// Once runs its setup once from two goroutines. Correct.
func Once() int {
	var once sync.Once
	var wg sync.WaitGroup
	n := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			once.Do(func() { n++ })
		}()
	}
	wg.Wait()
	return n
}

// gen sends 0..n-1 on the channel it returns and closes it.
func gen(n int) <-chan int {
	ch := make(chan int)
	go func() {
		defer close(ch)
		for i := 0; i < n; i++ {
			ch <- i
		}
	}()
	return ch
}

// SumGen adds what gen sends. Correct.
func SumGen(n int) int {
	if n > 4 {
		n = 4
	}
	s := 0
	for v := range gen(n) {
		s += v
	}
	return s
}

// CountAtomic counts three goroutines with an atomic counter. Correct.
func CountAtomic() int32 {
	var n int32
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			atomic.AddInt32(&n, 1)
		}()
	}
	wg.Wait()
	return atomic.LoadInt32(&n)
}

// reg and tally are package variables with a mutex: every goroutine and
// every call shares them.
var reg struct {
	sync.Mutex
	n int
}

var tally struct {
	sync.Mutex
	n int
}

// Bump counts in reg. Bug: Bump(true) locks reg's mutex twice and blocks
// forever (a self-deadlock on a package-level mutex).
func Bump(again bool) int {
	reg.Lock()
	reg.n++
	if again {
		reg.Lock()
	}
	n := reg.n
	reg.Unlock()
	return n
}

// BumpAll counts three goroutines in tally. Correct.
func BumpAll() int {
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tally.Lock()
			tally.n++
			tally.Unlock()
		}()
	}
	wg.Wait()
	tally.Lock()
	defer tally.Unlock()
	return tally.n
}

// lazy holds a channel made on first use.
type lazy struct {
	mu sync.Mutex
	ch chan int
}

// LazyClose makes l's channel on first use, and a second goroutine closes
// it if it was made. Race-free and never blocks: the channel exists when
// the second goroutine looks, so it is closed before the final receive.
func LazyClose() bool {
	l := &lazy{}
	l.mu.Lock()
	if l.ch == nil {
		l.ch = make(chan int, 1)
	}
	l.mu.Unlock()
	done := make(chan bool)
	go func() {
		l.mu.Lock()
		if l.ch != nil {
			close(l.ch)
		}
		l.mu.Unlock()
		done <- true
	}()
	<-done
	_, ok := <-l.ch
	return ok
}
