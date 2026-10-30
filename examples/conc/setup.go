package conc

import "sync"

// Tally keeps a total and a table of slots it fills when made: setup a
// concurrent harness runs before its goroutines start.
type Tally struct {
	mu    sync.Mutex
	slots []int
	total int
}

func NewTally(n int) *Tally {
	t := &Tally{slots: make([]int, n)}
	for i := range t.slots {
		t.slots[i] = i
	}
	return t
}

// AddRacy adds v without the lock.
func (t *Tally) AddRacy(v int) {
	cur := t.total
	t.total = cur + v
}

// Add adds v under the lock.
func (t *Tally) Add(v int) {
	t.mu.Lock()
	t.total += v
	t.mu.Unlock()
}

// go2lean:sequence Tally.AddRacy
// SeqConcTallyRacy: two concurrent AddRacy calls must sum. The tally is
// made before anything reads a or b, so the search runs that setup once
// (#87).
func SeqConcTallyRacy(a int, b int) bool {
	t := NewTally(32)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); t.AddRacy(a) }()
	go func() { defer wg.Done(); t.AddRacy(b) }()
	wg.Wait()
	return t.total == a+b
}

// go2lean:sequence Tally.Add
// SeqConcTally: two concurrent Add calls sum.
func SeqConcTally(a int, b int) bool {
	t := NewTally(32)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); t.Add(a) }()
	go func() { defer wg.Done(); t.Add(b) }()
	wg.Wait()
	return t.total == a+b
}
