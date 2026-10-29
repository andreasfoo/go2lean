package semantics

import "sync/atomic"

// go2lean's own tests (#67): the methods of sync/atomic's typed values.

func testAtomicUint32CompareAndSwap() bool {
	var x atomic.Uint32
	x.Store(1)
	first := x.CompareAndSwap(1, 2)
	second := x.CompareAndSwap(1, 3)
	return first && !second && x.Load() == 2
}

func testAtomicBoolSwap() bool {
	var b atomic.Bool
	old := b.Swap(true)
	return !old && b.Load() && b.CompareAndSwap(true, false) && !b.Load()
}

func testAtomicInt32Swap() bool {
	var x atomic.Int32
	x.Store(5)
	return x.Swap(7) == 5 && x.Load() == 7
}

func testAtomicInt64AndOr() bool {
	var x atomic.Int64
	x.Store(0b1100)
	a := x.And(0b1010)
	o := x.Or(0b0001)
	return a == 0b1100 && o == 0b1000 && x.Load() == 0b1001
}
