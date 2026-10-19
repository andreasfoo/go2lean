package iface

// go2lean:spec Queue.Push
// SpecQueuePushLast: after Push(v) the last item is the int v.
func SpecQueuePushLast(q *Queue, v int) bool {
	if q == nil {
		return true
	}
	n := len(q.items)
	if n == 0 {
		return false
	}
	x, ok := q.items[n-1].(int)
	return ok && x == v
}

// go2lean:spec Count
// SpecCountOne: exactly one int is counted.
func SpecCountOne(n int, name string, ret int) bool {
	return ret == 1
}

// go2lean:spec Pipe.Keep
// SpecPipeKeepUnfiltered: without a filter, Keep appends s.
func SpecPipeKeepUnfiltered(p *Pipe, s string) bool {
	if p == nil || (p.hooks != nil && p.hooks.Filter != nil) {
		return true
	}
	n := len(p.kept)
	return n > 0 && p.kept[n-1] == s
}
