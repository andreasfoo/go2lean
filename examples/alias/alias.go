// Package alias is a ground-truth example for go2lean's heap model: writes
// through slices and pointers, with the bugs visible only in the mutated
// arguments.
package alias

// Account is a balance with a limit.
type Account struct {
	Balance int
	Limit   int
}

// Deposit adds amt to the balance when it would not exceed the limit. The
// check is made against the old balance, so a deposit can push the balance
// past the limit. It also dereferences a nil account.
func Deposit(a *Account, amt int) bool {
	if amt < 0 {
		return false
	}
	if a.Balance > a.Limit {
		return false
	}
	a.Balance += amt
	return true
}

// SpecDepositWithinLimit: a successful deposit leaves Balance within Limit.
//
// go2lean:spec Deposit
func SpecDepositWithinLimit(a *Account, amt int, ret bool) bool {
	if !ret {
		return true
	}
	return a.Balance <= a.Limit
}

// Fill sets every element of xs to v. The loop bound misses the last element.
func Fill(xs []int, v int) {
	for i := 0; i < len(xs)-1; i++ {
		xs[i] = v
	}
}

// SpecFillLast: after Fill, the last element holds v.
//
// go2lean:spec Fill
func SpecFillLast(xs []int, v int) bool {
	if len(xs) == 0 {
		return true
	}
	return xs[len(xs)-1] == v
}

// Swap exchanges the two pointees; it is correct, including when p == q.
func Swap(p, q *int) {
	t := *p
	*p = *q
	*q = t
}
