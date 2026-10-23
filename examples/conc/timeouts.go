package conc

import (
	"context"
	"sync"
	"time"
)

// Contexts, timers and condition variables. As in conc.go, each function is
// either correct under every schedule or has one bug, named in its comment.

// work sends n on out, or gives up when ctx ends.
func work(ctx context.Context, n int, out chan<- int) {
	select {
	case out <- n:
	case <-ctx.Done():
	}
}

// Cancelled starts a worker and cancels it after taking its result or not.
// Correct: the worker gives up on cancellation.
func Cancelled(take bool) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan int)
	go work(ctx, 7, out)
	if take {
		return <-out
	}
	return 0
}

// WithDeadline waits for a result or the deadline. Bug: the result channel
// is unbuffered and the sender does not watch ctx, so when the deadline
// passes first the sender blocks forever (a goroutine leak).
func WithDeadline() int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	out := make(chan int)
	go func() { time.Sleep(time.Millisecond); out <- 1 }()
	select {
	case v := <-out:
		return v
	case <-ctx.Done():
		return -1
	}
}

// Timeout waits for a result or time.After. Bug: as WithDeadline, the
// sender leaks when the timer fires first.
func Timeout() int {
	out := make(chan int)
	go func() { time.Sleep(time.Millisecond); out <- 1 }()
	select {
	case v := <-out:
		return v
	case <-time.After(time.Nanosecond):
		return -1
	}
}

// TimeoutBuffered is Timeout with a buffered result channel. Correct.
func TimeoutBuffered() int {
	out := make(chan int, 1)
	go func() { time.Sleep(time.Millisecond); out <- 1 }()
	select {
	case v := <-out:
		return v
	case <-time.After(time.Nanosecond):
		return -1
	}
}

// Poll counts ticks until stop is closed. Correct: the ticker does not
// keep the program alive.
func Poll() int {
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	stop := make(chan struct{})
	go func() { time.Sleep(time.Millisecond); close(stop) }()
	n := 0
	for {
		select {
		case <-t.C:
			n++
		case <-stop:
			return n
		}
	}
}

// queue is a one-slot queue on a condition variable.
type queue struct {
	mu    sync.Mutex
	cond  *sync.Cond
	items []int
}

func newQueue() *queue {
	q := &queue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *queue) put(v int) {
	q.mu.Lock()
	q.items = append(q.items, v)
	q.mu.Unlock()
	q.cond.Signal()
}

// take waits in a loop until an item is there. Correct.
func (q *queue) take() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 {
		q.cond.Wait()
	}
	v := q.items[0]
	q.items = q.items[1:]
	return v
}

// takeOnce waits once if the queue is empty. Bug: a Signal sent before the
// Wait is lost (no item check after it), so it can wait forever.
func (q *queue) takeOnce() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cond.Wait()
	return len(q.items) > 0
}

// Handoff passes a value through the queue. Correct.
func Handoff(v int) int {
	q := newQueue()
	go q.put(v)
	return q.take()
}

// LostWakeup passes a value with takeOnce. Bug: when put runs first, its
// Signal wakes nobody and takeOnce waits forever (a deadlock).
func LostWakeup() bool {
	q := newQueue()
	go q.put(1)
	time.Sleep(time.Millisecond)
	return q.takeOnce()
}
