package ir

import (
	"reflect"
	"testing"
)

func TestLoopHeaders(t *testing.T) {
	// entry -> 3 (header) -> 1 (body) -> 3; 3 -> 2 (exit), as go/ssa lays
	// out `for i < n { i++ }`; then an if-else that rejoins (no loop).
	loop := &Func{Entry: 0, Blocks: []Block{
		{Term: Term{Kind: TermJump, B: 3}},
		{Term: Term{Kind: TermJump, B: 3}},
		{Term: Term{Kind: TermRet}},
		{Term: Term{Kind: TermBr, T: 1, F: 2}},
	}}
	if h := loop.LoopHeaders(); !reflect.DeepEqual(h, []int{3}) {
		t.Errorf("loop: got %v", h)
	}
	diamond := &Func{Entry: 0, Blocks: []Block{
		{Term: Term{Kind: TermBr, T: 1, F: 2}},
		{Term: Term{Kind: TermJump, B: 3}},
		{Term: Term{Kind: TermJump, B: 3}},
		{Term: Term{Kind: TermRet}},
	}}
	if h := diamond.LoopHeaders(); h != nil {
		t.Errorf("diamond: got %v", h)
	}
	// A call continues in Next; a loop through it is found.
	call := &Func{Entry: 0, Blocks: []Block{
		{Term: Term{Kind: TermCall, Next: 1}},
		{Term: Term{Kind: TermBr, T: 0, F: 2}},
		{Term: Term{Kind: TermRet}},
	}}
	if h := call.LoopHeaders(); !reflect.DeepEqual(h, []int{0}) {
		t.Errorf("call: got %v", h)
	}
}
