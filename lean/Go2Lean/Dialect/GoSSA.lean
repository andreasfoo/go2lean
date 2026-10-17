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
  /-- Multiple results of a call or return. -/
  | tuple (vs : List Val)
  /-- An opaque `error` value: only nil-ness is modelled. -/
  | err (nonNil : Bool)
  /-- A pointer: heap object `ref` (0 is nil) and a path of field/element
  indices inside it. -/
  | ptr (ref : Nat) (path : List Nat)
  /-- A struct value (also the heap object for an allocated struct). -/
  | structv (fields : List Val)
  /-- A heap array: the backing store of a slice or an allocated array. -/
  | arr (elems : List Val)
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
  /-- Pack the operands as a tuple (multiple return values). -/
  | mkTuple
  /-- Component `i` of a tuple. -/
  | extract (i : Nat)
  -- heap (stage B)
  /-- Allocate a copy of `zero` on the heap; returns a pointer to it. -/
  | alloc (zero : Val)
  /-- `*p` for a pointer to a scalar or struct. -/
  | loadPath
  /-- `*p = v`; args `[p, v]`, returns `unit`. -/
  | storePath
  /-- `&p.f_i`; args `[p]`. -/
  | fieldAddr (i : Nat)
  /-- `&xs[i]` for a slice whose backing array is on the heap; args `[xs, i]`. -/
  | indexAddr
  /-- `s.f_i` of a struct value. -/
  | field (i : Nat)
  /-- A struct value from its fields. -/
  | mkStruct
  /-- `make([]T, len, cap)` filled with `zero`; args `[len, cap]`. -/
  | makeSlice (zero : Val)
  /-- `p == nil` style comparisons go through `eq`/`ne`; this reads `p == nil`. -/
  | isNil
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

/-- Equality where Go defines it for modelled values. Two non-nil errors
compare equal (an over-approximation of identity; M3 filters the fallout). -/
def eqVal : Val → Val → Option Bool
  | .int w a, .int w' b => if h : w' = w then some (a = (h ▸ b)) else none
  | .bool a, .bool b => some (a == b)
  | .err a, .err b => some (a == b)
  | .ptr r p, .ptr r' p' => some (r == r' && p == p')
  | _, _ => none

-- Heap navigation ------------------------------------------------------------

/-- Read the value at `path` inside object `v`. -/
def getPath : Val → List Nat → Option Val
  | v, [] => some v
  | .structv fs, i :: rest => fs[i]? >>= (getPath · rest)
  | .arr es, i :: rest => es[i]? >>= (getPath · rest)
  | _, _ => none

/-- Replace the value at `path` inside object `v`. -/
def setPath : Val → List Nat → Val → Option Val
  | _, [], nv => some nv
  | .structv fs, i :: rest, nv =>
    fs[i]? >>= fun f => (setPath f rest nv).map fun f' => .structv (fs.set i f')
  | .arr es, i :: rest, nv =>
    es[i]? >>= fun e => (setPath e rest nv).map fun e' => .arr (es.set i e')
  | _, _, _ => none

/-- Read through a pointer. `ref = 0` is nil. -/
def heapLoad (heap : Heap Val) : Val → Res Val
  | .ptr 0 _ => .panic .nilDereference
  | .ptr ref path =>
    match heap[ref]? >>= (getPath · path) with
    | some v => .ok v
    | none => .panic .modelStuck
  | _ => .panic .modelStuck

/-- Write through a pointer. -/
def heapStore (heap : Heap Val) : Val → Val → Res (Heap Val)
  | .ptr 0 _, _ => .panic .nilDereference
  | .ptr ref path, nv =>
    match heap[ref]? >>= (setPath · path nv) with
    | some obj => .ok (heap.set! ref obj)
    | none => .panic .modelStuck
  | _, _ => .panic .modelStuck

/-- Allocate `obj`; the heap's slot 0 is nil, so the first real object gets
reference 1. -/
def heapAlloc (heap : Heap Val) (obj : Val) : Val × Heap Val :=
  let heap := if heap.size = 0 then #[.unit] else heap
  (.ptr heap.size [], heap.push obj)

/-- Go index semantics: a negative index is out of range, so compare unsigned
after sign-extending to 64 bits. -/
def asIndex : Val → Option (BitVec 64)
  | .int _ i => some (i.signExtend 64)
  | _ => none

/-- Pure instructions: no heap access. -/
def denotePure (or : Oracle) (heap : Heap Val) : Op → List Val → Res Val
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
      if i.ult len then
        -- A backing array on the heap (materialised input or allocation)
        -- is authoritative; anything else reads the oracle.
        match heap[ref]? with
        | some (.arr es) =>
          match es[(off + i).toNat]? with
          | some (.int _ v) => .ok (.int w (v.zeroExtend w))
          | some v => .ok v
          | none => .panic .modelStuck
        | _ => .ok (.int w ((or.mem ref (off + i)).zeroExtend w))
      else .panic .indexOutOfRange
    | none => .panic .modelStuck
  | .field i, [.structv fs] =>
    match fs[i]? with
    | some v => .ok v
    | none => .panic .modelStuck
  | .mkStruct, fs => .ok (.structv fs)
  | .fieldAddr i, [.ptr 0 _] => .ok (.ptr 0 [i]) -- &nil.f stays nil-based; deref panics
  | .fieldAddr i, [.ptr ref path] => .ok (.ptr ref (path ++ [i]))
  | .indexAddr, [.slice ref off len _, i] =>
    match asIndex i with
    | some i => if i.ult len then .ok (.ptr ref [(off + i).toNat]) else .panic .indexOutOfRange
    | none => .panic .modelStuck
  | .isNil, [.ptr r _] => .ok (.bool (r == 0))
  | .isNil, [.err nonNil] => .ok (.bool !nonNil)
  | .mkTuple, vs => .ok (.tuple vs)
  | .extract i, [.tuple vs] =>
    match vs[i]? with
    | some v => .ok v
    | none => .panic .modelStuck
  | .reslice, [.slice ref off _ cap, lo, hi] =>
    match asIndex lo, asIndex hi with
    | some lo, some hi =>
      if lo.ule hi && hi.ule cap then .ok (.slice ref (off + lo) (hi - lo) (cap - lo))
      else .panic .sliceBounds
    | _, _ => .panic .modelStuck
  | _, _ => .panic .modelStuck

/-- All instructions: heap-touching ones here, the rest via `denotePure`. -/
def denote (or : Oracle) (heap : Heap Val) : Op → List Val → Res (Val × Heap Val)
  | .alloc zero, [] => .ok (heapAlloc heap zero)
  | .loadPath, [p] =>
    match heapLoad heap p with
    | .ok v => .ok (v, heap)
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  | .storePath, [p, v] =>
    match heapStore heap p v with
    | .ok heap => .ok (.unit, heap)
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  | .makeSlice zero, [len, cap] =>
    match asIndex len, asIndex cap with
    | some len, some cap =>
      if len.ule cap && cap.toNat ≤ 1024 then
        let (p, heap) := heapAlloc heap (.arr (List.replicate cap.toNat zero))
        match p with
        | .ptr ref _ => .ok (.slice ref 0#64 len cap, heap)
        | _ => .panic .modelStuck
      else if len.ule cap then .panic .modelStuck -- too large to materialise
      else .panic .makeSliceLen
    | _, _ => .panic .modelStuck
  | op, args =>
    match denotePure or heap op args with
    | .ok v => .ok (v, heap)
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel

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
