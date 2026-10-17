import Go2Lean.SSA.Core

/-!
# GoSSA dialect

Stage A of the `go/ssa` dialect: integers of every Go width, booleans and
read-only slices. Slice contents are not modelled; the frontend emits a
bounds check (`indexCheck`) followed by a `havoc` for the loaded value.

Semantics follow the Go specification, not LLVM: integer division by zero
panics, `MinInt / -1` wraps, shifts by at least the width give 0 (or the sign
fill), negative signed shift counts panic.
-/
namespace Go2Lean.GoSSA

open Go2Lean.SSA

inductive Val where
  | int (w : Nat) (v : BitVec w)
  | bool (b : Bool)
  /-- A slice header. `ref` abstracts the backing array; contents are havoc. -/
  | slice (ref : Nat) (off len cap : BitVec 64)
  | unit
  deriving Repr

instance : Inhabited Val := ⟨.unit⟩

/-- Read-only memory: backing array `ref`, element index → 64-bit value. Loads
of narrower element types truncate. Fixed for a whole run, so every function
in the program sees the same slice contents. -/
structure Oracle where
  mem : Nat → BitVec 64 → BitVec 64

inductive Op where
  | const (v : Val)
  -- integer arithmetic (same width on both operands)
  | add | sub | mul
  | sdiv | udiv | srem | urem
  | and | or | xor | andNot
  /-- `x << n`; `countSigned` says whether `n` has a signed Go type. -/
  | shl (countSigned : Bool)
  /-- `x >> n`; arithmetic if `signed`. -/
  | shr (signed countSigned : Bool)
  -- comparisons; `s*`/`u*` are signed/unsigned orderings
  | eq | ne
  | slt | sle | sgt | sge
  | ult | ule | ugt | uge
  -- unary
  | neg | bnot | not
  /-- Integer conversion to width `w`; `signExtend` iff the source type is signed. -/
  | conv (w : Nat) (signExtend : Bool)
  -- slices
  | len | cap
  /-- `xs[i]` bounds check. Panics on failure, returns `unit`. -/
  | indexCheck
  /-- `xs[i]` for a slice of `w`-bit integers: bounds check, then read memory. -/
  | load (w : Nat)
  /-- `xs[lo:hi]` -/
  | reslice
  deriving Repr

/-- Lift a width-polymorphic binary function to two `int` values of equal width. -/
@[inline] def binInt (f : {w : Nat} → BitVec w → BitVec w → BitVec w) : List Val → Res Val
  | [.int w a, .int w' b] =>
    if h : w' = w then .ok (.int w (f a (h ▸ b))) else .panic .modelStuck
  | _ => .panic .modelStuck

@[inline] def cmpInt (f : {w : Nat} → BitVec w → BitVec w → Bool) : List Val → Res Val
  | [.int w a, .int w' b] =>
    if h : w' = w then .ok (.bool (f a (h ▸ b))) else .panic .modelStuck
  | _ => .panic .modelStuck

/-- Division helpers: Go panics on a zero divisor. -/
@[inline] def divInt (f : {w : Nat} → BitVec w → BitVec w → BitVec w) : List Val → Res Val
  | [.int w a, .int w' b] =>
    if h : w' = w then
      if (h ▸ b) = 0#w then .panic .divideByZero else .ok (.int w (f a (h ▸ b)))
    else .panic .modelStuck
  | _ => .panic .modelStuck

/-- Shift count as a natural number, or `negativeShift` for a negative signed count. -/
def shiftCount (countSigned : Bool) : Val → Res Nat
  | .int _ n => if countSigned && n.msb then .panic .negativeShift else .ok n.toNat
  | _ => .panic .modelStuck

def eqVal : Val → Val → Option Bool
  | .int w a, .int w' b => if h : w' = w then some (a = (h ▸ b)) else none
  | .bool a, .bool b => some (a == b)
  | _, _ => none

/-- Go index semantics: a negative index is out of range, so compare unsigned
after sign-extending to 64 bits. -/
def asIndex : Val → Option (BitVec 64)
  | .int _ i => some (i.signExtend 64)
  | _ => none

def denote (or : Oracle) : Op → List Val → Res Val
  | .const v, [] => .ok v
  | .add, args => binInt (· + ·) args
  | .sub, args => binInt (· - ·) args
  | .mul, args => binInt (· * ·) args
  | .sdiv, args => divInt BitVec.sdiv args
  | .udiv, args => divInt BitVec.udiv args
  | .srem, args => divInt BitVec.srem args
  | .urem, args => divInt BitVec.umod args
  | .and, args => binInt (· &&& ·) args
  | .or, args => binInt (· ||| ·) args
  | .xor, args => binInt (· ^^^ ·) args
  | .andNot, args => binInt (fun a b => a &&& ~~~b) args
  | .shl cs, [.int w a, n] =>
    match shiftCount cs n with
    | .ok k => .ok (.int w (a <<< k))
    | .panic p => .panic p
    | .outOfFuel => .outOfFuel
  | .shr signed cs, [.int w a, n] =>
    match shiftCount cs n with
    | .ok k => .ok (.int w (if signed then a.sshiftRight k else a >>> k))
    | .panic p => .panic p
    | .outOfFuel => .outOfFuel
  | .eq, [a, b] => match eqVal a b with | some r => .ok (.bool r) | none => .panic .modelStuck
  | .ne, [a, b] => match eqVal a b with | some r => .ok (.bool !r) | none => .panic .modelStuck
  | .slt, args => cmpInt BitVec.slt args
  | .sle, args => cmpInt BitVec.sle args
  | .sgt, args => cmpInt (fun a b => BitVec.slt b a) args
  | .sge, args => cmpInt (fun a b => BitVec.sle b a) args
  | .ult, args => cmpInt BitVec.ult args
  | .ule, args => cmpInt BitVec.ule args
  | .ugt, args => cmpInt (fun a b => BitVec.ult b a) args
  | .uge, args => cmpInt (fun a b => BitVec.ule b a) args
  | .neg, [.int w a] => .ok (.int w (-a))
  | .bnot, [.int w a] => .ok (.int w (~~~a))
  | .not, [.bool b] => .ok (.bool !b)
  | .conv w' se, [.int _ a] =>
    .ok (.int w' (if se then a.signExtend w' else a.zeroExtend w'))
  | .len, [.slice _ _ len _] => .ok (.int 64 len)
  | .cap, [.slice _ _ _ cap] => .ok (.int 64 cap)
  | .indexCheck, [.slice _ _ len _, i] =>
    match asIndex i with
    | some i => if i.ult len then .ok .unit else .panic .indexOutOfRange
    | none => .panic .modelStuck
  | .load w, [.slice ref off len _, i] =>
    match asIndex i with
    | some i =>
      if i.ult len then .ok (.int w ((or.mem ref (off + i)).zeroExtend w))
      else .panic .indexOutOfRange
    | none => .panic .modelStuck
  | .reslice, [.slice ref off _ cap, lo, hi] =>
    match asIndex lo, asIndex hi with
    | some lo, some hi =>
      if lo.ule hi && hi.ule cap then .ok (.slice ref (off + lo) (hi - lo) (cap - lo))
      else .panic .sliceBounds
    | _, _ => .panic .modelStuck
  | _, _ => .panic .modelStuck

instance : Dialect Op Val Oracle where
  denote := denote
  toBool
    | .bool b => some b
    | _ => none

abbrev Prog := SSA.Prog Op
abbrev Func := SSA.Func Op
abbrev Block := SSA.Block Op
abbrev Instr := SSA.Instr Op

end Go2Lean.GoSSA
