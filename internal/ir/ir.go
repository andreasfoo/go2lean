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
	"strings"

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

// ErrVal is an opaque error value; only nil-ness is modelled.
type ErrVal struct{ NonNil bool }

func (v ErrVal) Lean() string { return fmt.Sprintf("(.err %v)", v.NonNil) }

// NilPtr is the nil pointer.
type NilPtr struct{}

func (NilPtr) Lean() string { return "(.ptr 0 [])" }

// NilSlice is the nil slice.
type NilSlice struct{}

func (NilSlice) Lean() string { return "(.slice 0 0#64 0#64 0#64)" }

// StructVal is a struct value from its field values.
type StructVal struct{ Fields []Val }

func (v StructVal) Lean() string {
	parts := make([]string, len(v.Fields))
	for i, f := range v.Fields {
		parts[i] = f.Lean()
	}
	return "(.structv [" + strings.Join(parts, ", ") + "])"
}

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

// Extract takes component I of a tuple.
type Extract struct{ I int }

func (e Extract) Lean() string { return fmt.Sprintf("(.extract %d)", e.I) }

// Alloc allocates a heap object initialised to Zero and yields a pointer.
type Alloc struct{ Zero Val }

func (a Alloc) Lean() string { return "(.alloc " + a.Zero.Lean() + ")" }

// FieldAddr is &p.f_I for a pointer p.
type FieldAddr struct{ I int }

func (f FieldAddr) Lean() string { return fmt.Sprintf("(.fieldAddr %d)", f.I) }

// Field is s.f_I for a struct value s.
type Field struct{ I int }

func (f Field) Lean() string { return fmt.Sprintf("(.field %d)", f.I) }

// MakeSlice is make([]T, len, cap) with elements Zero.
type MakeSlice struct{ Zero Val }

func (m MakeSlice) Lean() string { return "(.makeSlice " + m.Zero.Lean() + ")" }

// Havoc site kinds, encoded in Prog.HavocWidths: 0 is an opaque error, 1 a
// bool, any other value the width of an integer.
const (
	HavocError = 0
	HavocBool  = 1
)

// Instr is `Dst := Op(Args)`, or a havoc when Op is nil.
type Instr struct {
	Dst       Var
	Op        Op
	Args      []Var
	HavocSite int
}

type TermKind int

const (
	// TermNone is the zero value: a block the frontend never terminated.
	// Validate rejects it so a lost write cannot become a silent wrong model.
	TermNone TermKind = iota
	TermRet
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
	Consts  []string // integer literals, see contract.ModelFunc.Consts
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

// Validate checks structural invariants the Lean printer and interpreter
// rely on: every block has a terminator, and every block, function and
// variable reference is in range.
func (p *Prog) Validate() error {
	for fi, f := range p.Funcs {
		checkVar := func(v Var, where string) error {
			if int(v) < 0 || int(v) >= f.NVals {
				return fmt.Errorf("%s: %s references variable %d outside 0..%d", f.Name, where, v, f.NVals-1)
			}
			return nil
		}
		checkBlock := func(b int, where string) error {
			if b < 0 || b >= len(f.Blocks) {
				return fmt.Errorf("%s: %s targets block %d of %d", f.Name, where, b, len(f.Blocks))
			}
			return nil
		}
		if f.Status == contract.ModelRejected {
			continue
		}
		if err := checkBlock(f.Entry, "entry"); err != nil {
			return err
		}
		for bi, blk := range f.Blocks {
			where := fmt.Sprintf("block %d (%s)", bi, blk.Name)
			for _, in := range blk.Instrs {
				if err := checkVar(in.Dst, where); err != nil {
					return err
				}
				for _, a := range in.Args {
					if err := checkVar(a, where); err != nil {
						return err
					}
				}
			}
			t := blk.Term
			switch t.Kind {
			case TermNone:
				return fmt.Errorf("%s: %s has no terminator (frontend bug)", f.Name, where)
			case TermRet:
				if err := checkVar(t.V, where+" ret"); err != nil {
					return err
				}
			case TermJump:
				if err := checkBlock(t.B, where+" jump"); err != nil {
					return err
				}
			case TermBr:
				if err := checkBlock(t.T, where+" br"); err != nil {
					return err
				}
				if err := checkBlock(t.F, where+" br"); err != nil {
					return err
				}
			case TermCall:
				if t.Callee < 0 || t.Callee >= len(p.Funcs) {
					return fmt.Errorf("%s: %s calls function %d of %d", f.Name, where, t.Callee, len(p.Funcs))
				}
				if err := checkBlock(t.Next, where+" call continuation"); err != nil {
					return err
				}
				if err := checkVar(t.Dst, where+" call result"); err != nil {
					return err
				}
			}
		}
		_ = fi
	}
	return nil
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
