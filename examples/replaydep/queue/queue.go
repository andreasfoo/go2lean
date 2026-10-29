// Package queue is a mutex-guarded queue, a dependency of replaydep: its
// lock is a step of the model's schedule that the replay, which rewrites
// only the package under test, does not gate (#56).
package queue

import "sync"

// Queue is a FIFO of ints, safe for concurrent use.
type Queue struct {
	mu sync.Mutex
	xs []int
}

// Push appends x.
func (q *Queue) Push(x int) {
	q.mu.Lock()
	q.xs = append(q.xs, x)
	q.mu.Unlock()
}

// Len is the number of values pushed.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.xs)
}
