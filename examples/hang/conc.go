package hang

import "sync"

// Relay passes a value between two goroutines until one of them sees n,
// and returns it. The value only ever alternates between 0 and 1, so for
// any other n the two goroutines pass it back and forth forever while the
// caller waits: a livelock.
func Relay(n int) int {
	a, b, done := make(chan int), make(chan int), make(chan int)
	go func() {
		for v := range a {
			if v == n {
				close(b)
				done <- v
				return
			}
			b <- v
		}
	}()
	go func() {
		for v := range b {
			a <- 1 - v
		}
	}()
	a <- 0
	return <-done
}

// Handoff waits under a mutex for a flag another goroutine sets. It only
// loops under a schedule that never runs that goroutine, which Go's
// scheduler does not keep taking: no livelock.
func Handoff() int {
	var mu sync.Mutex
	ready := false
	go func() {
		mu.Lock()
		ready = true
		mu.Unlock()
	}()
	for {
		mu.Lock()
		if ready {
			mu.Unlock()
			return 1
		}
		mu.Unlock()
	}
}
