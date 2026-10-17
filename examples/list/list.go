// Package list is a ground-truth example for recursive types: a singly
// linked list built from pointers.
package list

// Node is a list cell.
type Node struct {
	Val  int
	Next *Node
}

// Len counts the cells; correct.
func Len(n *Node) int {
	c := 0
	for n != nil {
		c++
		n = n.Next
	}
	return c
}

// Sum adds the values. It skips the head cell.
func Sum(n *Node) int {
	s := 0
	if n == nil {
		return 0
	}
	for p := n.Next; p != nil; p = p.Next {
		s += p.Val
	}
	return s
}

// SpecSumSingleton: a one-cell list sums to its value.
//
// go2lean:spec Sum
func SpecSumSingleton(n *Node, ret int) bool {
	if n == nil || n.Next != nil {
		return true
	}
	return ret == n.Val
}
