// Package ir is the frontend-independent program representation M1's
// frontends produce and the Lean printer consumes. It mirrors the Lean SSA
// core (Go2Lean.SSA): functions of basic blocks with block parameters, a
// dialect op per instruction, and a terminator.
//
// Ops and constants are kept as small structs rather than Lean strings so
// that a second frontend (LLVM) can target the same printer.
package ir

import (
	"fmt"

	"github.com/andreasfoo/go2lean/internal/contract"
)

// Var is an index into a function's value environment.
type Var int

// Val is a dialect constant.
type Val interface{ Lean() string }

// IntVal is a w-bit integer; Bits holds the two's-complement pattern as a
// non-negative decimal string.
type IntVal struct {
	Width int
	Bits  string
}

func (v IntVal) Lean() string { return fmt.Sprintf("(.int %d %s#%d)", v.Width, v.Bits, v.Width) }

type BoolVal bool

func (v BoolVal) Lean() string { return fmt.Sprintf("(.bool %v)", bool(v)) }

type UnitVal struct{}

func (UnitVal) Lean() string { return ".unit" }

// Op is a dialect instruction.
type Op interface{ Lean() string }

// Const materialises a constant.
type Const struct{ V Val }

func (c Const) Lean() string { return "(.const " + c.V.Lean() + ")" }

// Simple is a nullary-constructor op such as add, slt, len.
type Simple string

func (s Simple) Lean() string { return "." + string(s) }

// Shl is `x << n`; CountSigned says whether n has a signed type.
type Shl struct{ CountSigned bool }

func (s Shl) Lean() string { return fmt.Sprintf("(.shl %v)", s.CountSigned) }

// Shr is `x >> n`, arithmetic when Signed.
type Shr struct{ Signed, CountSigned bool }

func (s Shr) Lean() string { return fmt.Sprintf("(.shr %v %v)", s.Signed, s.CountSigned) }

// Conv converts an integer to Width bits, sign-extending if SignExtend.
type Conv struct {
	Width      int
	SignExtend bool
}

func (c Conv) Lean() string { return fmt.Sprintf("(.conv %d %v)", c.Width, c.SignExtend) }

// Load reads a Width-bit element: args are [slice, index].
type Load struct{ Width int }

func (l Load) Lean() string { return fmt.Sprintf("(.load %d)", l.Width) }

// Instr is `Dst := Op(Args)`, or a havoc when Op is nil.
type Instr struct {
	Dst       Var
	Op        Op
	Args      []Var
	HavocSite int
}

type TermKind int

const (
	TermRet TermKind = iota
	TermJump
	TermBr
	TermPanic
	TermCall
)

// Term is a block terminator. Which fields are meaningful depends on Kind.
type Term struct {
	Kind  TermKind
	V     Var   // ret
	Cond  Var   // br
	B     int   // jump target
	Args  []Var // jump args
	T, F  int   // br targets
	TArgs []Var
	FArgs []Var
	Panic contract.PanicKind
	// call
	Callee int
	CArgs  []Var
	Dst    Var
	Next   int
	NArgs  []Var
}

type Block struct {
	Name   string // for comments only
	Params []Var
	Instrs []Instr
	Term   Term
}

type Func struct {
	Name   string
	GoSig  string
	Params []Var
	NVals  int
	Entry  int
	Blocks []Block

	Status  contract.ModelStatus
	Issues  []string
	SpecFor string
	ABI     contract.ABI
}

// Prog is one package's worth of functions. Func indices are the Lean FuncIds.
type Prog struct {
	Package     string
	Frontend    string
	Funcs       []*Func
	HavocWidths []int
	Havoc       []contract.ABIHavoc
}

// FuncID returns the index of the function named name, or -1.
func (p *Prog) FuncID(name string) int {
	for i, f := range p.Funcs {
		if f.Name == name {
			return i
		}
	}
	return -1
}
