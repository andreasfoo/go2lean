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

// go2lean:spec Area
// SpecAreaDispatch: a square for small sides, a 2-high rectangle above 10.
func SpecAreaDispatch(n int, ret int) bool {
	if n > 10 {
		return ret == 2*n
	}
	return ret == n*n
}

// go2lean:spec Name
// SpecNameOmitted: "none" exactly for the zero option, else its value.
func SpecNameOmitted(o Opt, ret string) bool {
	if o.V == "" && !o.Set {
		return ret == "none"
	}
	return ret == o.V
}

// go2lean:spec Set
// SpecSetNonNil: a message is set exactly when the pointer is not nil.
func SpecSetNonNil(m *Msg, ret bool) bool { return ret == (m != nil) }
