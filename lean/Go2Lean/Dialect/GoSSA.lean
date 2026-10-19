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
  /-- A string: `len` bytes starting at `off` in the immutable byte array
  `ref` (elements are `.int 8`). The empty string is `str 0 0 0`. -/
  | str (ref : Nat) (off len : BitVec 64)
  /-- A concrete value stored in an interface: `tag` names its dynamic type
  (a hash of the Go type's name, the same in every package). Interface
  values the model did not build itself (inputs, havoc) are `unit`. -/
  | boxed (tag : Nat) (v : Val)
  /-- A nil func, map, channel or interface value. A non-nil one the
  program built itself is `boxed` (tag 0 for funcs, maps and channels);
  `unit` is one the model knows nothing about. -/
  | opaqueNil
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
  /-- `a[i]` of an array value; args `[a, i]`. -/
  | arrIndex
  /-- A struct value from its fields. -/
  | mkStruct
  /-- `make([]T, len, cap)` filled with `zero`; args `[len, cap]`. -/
  | makeSlice (zero : Val)
  /-- `append(s, t...)`: writes in place while the capacity allows, else
  copies into a fresh backing array; `zero` pads the new capacity. -/
  | appendSlice (zero : Val)
  /-- `copy(dst, src)`: copies `min len` elements, yields the count. -/
  | copySlice
  /-- `p == nil` style comparisons go through `eq`/`ne`; this reads `p == nil`. -/
  | isNil
  -- strings
  /-- A string literal (its UTF-8 bytes), allocated on the heap when evaluated. -/
  | strConst (bytes : List Nat)
  | strLen
  /-- `s[i]`: a byte. -/
  | strIndex
  /-- `s[lo:hi]`; args `[s, lo, hi]`. -/
  | strSlice
  | strConcat
  /-- `string(b)` for a byte slice; copies. -/
  | bytesToStr
  /-- `[]byte(s)`; copies. -/
  | strToBytes
  /-- `[]rune(s)`: UTF-8 decoded, invalid bytes become U+FFFD. -/
  | strToRunes
  /-- `string(rs)` for a slice of runes. -/
  | runesToStr
  /-- `string(r)` for one rune. -/
  | runeToStr
  /-- One step of `for i, r := range s`: the argument points at a heap cell
  holding `(s, pos)`; yields `(ok, i, r)` and advances the cell. -/
  | strNext
  /-- `s[lo:hi:max]`. -/
  | reslice3
  -- a hand-written core of the strings / strconv packages
  | strHasPrefix | strHasSuffix | strContains | strIndexOf
  | strTrimSpace | strTrimPrefix | strTrimSuffix
  /-- `strconv.Atoi`: `(int, error)`. -/
  | strAtoi
  /-- `strconv.Itoa`. -/
  | strItoa
  | strIndexByte | strLastIndex | strCount | strEqualFold
  | strTrim | strTrimLeft | strTrimRight
  | strToLower | strToUpper | strReplaceAll
  /-- `strings.Split`: a slice of strings. -/
  | strSplit
  /-- `strings.Fields`. -/
  | strFields
  /-- `strings.Join`; args `[elems, sep]`. -/
  | strJoin
  /-- `strings.Repeat`; a negative count panics as in Go. -/
  | strRepeat
  /-- `errors.New` / `fmt.Errorf`: a non-nil error, whatever the arguments. -/
  | errNew
  -- strings.Builder, modelled as a pointer to a string cell
  /-- `(*strings.Builder).WriteString`; args `[p, s]`; yields `(len s, nil)`. -/
  | sbWriteString
  /-- `WriteByte` / `WriteRune`; args `[p, c]`; yields `nil` resp. `(size, nil)`. -/
  | sbWriteByte | sbWriteRune
  /-- `String` / `Len` / `Reset`; arg `[p]`. -/
  | sbString | sbLen | sbReset
  -- interfaces
  /-- Store a concrete value of dynamic type `tag` in an interface. -/
  | box (tag : Nat)
  /-- Did the model build this interface value (so its dynamic type is known)? -/
  | isBoxed
  /-- `x.(T)` for a concrete `T` of tag `tag` on a boxed value; with
  `commaOk` the result is `(v, ok)` and a mismatch gives `(zero, false)`,
  without it a mismatch panics. -/
  | unbox (tag : Nat) (commaOk : Bool) (zero : Val)
  /-- Is it known whether this func/map/channel/interface value is nil? -/
  | nilKnown
  /-- Is this func/map/channel/interface value nil? (Only on known values.) -/
  | isNilOpaque
  -- maps the program builds: `.boxed 1 p`, `p` pointing at an `.arr` of
  -- `.tuple [key, value]` in insertion order
  | makeMap
  /-- `m[k]` (with `commaOk`, `(v, ok)`); `zero` for a missing key. -/
  | mapLookup (commaOk : Bool) (zero : Val)
  | mapUpdate | mapDelete | mapLen
  /-- An iterator over a map: a heap cell holding `(map, next index)`. -/
  | mapRange
  /-- Advance a map iterator: `(ok, key, value)`, zeros when done. -/
  | mapNext (zeroK zeroV : Val)
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
  | .boxed t v, .boxed t' v' => if t == t' then eqVal v v' else some false
  | .opaqueNil, .opaqueNil => some true
  | .opaqueNil, .boxed .. => some false
  | .boxed .., .opaqueNil => some false
  -- Go only compares slices against nil: equal when both are nil.
  | .slice r .., .slice r' .. => some ((r == 0) == (r' == 0))
  | .unit, .unit => none -- opaque values: the frontend havocs these comparisons
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

-- Strings --------------------------------------------------------------------

/-- The bytes of a string value, read from the heap. -/
def strBytes (heap : Heap Val) : Val → Option (List Nat)
  | .str _ _ 0#64 => some []
  | .str ref off len =>
    match heap[ref]? with
    | some (.arr es) =>
      some (((es.drop off.toNat).take len.toNat).map fun | .int _ v => v.toNat | _ => 0)
    | _ => none
  | _ => none

/-- Allocate a string with these bytes. -/
def strAlloc (heap : Heap Val) (bs : List Nat) : Val × Heap Val :=
  if bs.isEmpty then (.str 0 0#64 0#64, heap)
  else
    let (p, heap) := heapAlloc heap (.arr (bs.map fun b => Val.int 8 (BitVec.ofNat 8 b)))
    match p with
    | Val.ptr ref _ => (Val.str ref 0#64 (BitVec.ofNat 64 bs.length), heap)
    | _ => (Val.unit, heap)

def isSpaceByte (b : Nat) : Bool := b == 32 || (9 ≤ b && b ≤ 13)

/-- Index of the first occurrence of `pat` in `s`, or none. -/
def listIndexOf (s pat : List Nat) : Option Nat :=
  (List.range (s.length + 1)).find? fun i => (s.drop i).take pat.length == pat && i + pat.length ≤ s.length

/-- `strconv.Atoi` on bytes: optional sign, decimal digits, must fit int64. -/
def atoiBytes (bs : List Nat) : Option Int :=
  let (neg, digits) := match bs with
    | 45 :: rest => (true, rest)
    | 43 :: rest => (false, rest)
    | rest => (false, rest)
  if digits.isEmpty || !digits.all (fun d => 48 ≤ d && d ≤ 57) then none
  else
    let n : Nat := digits.foldl (fun acc d => acc * 10 + (d - 48)) 0
    let v : Int := if neg then -n else n
    if v < -(2 ^ 63) || v ≥ 2 ^ 63 then none else some v

def itoaBytes (v : Int) : List Nat :=
  let digits := (toString v.natAbs).toUTF8.toList.map (·.toNat)
  if v < 0 then 45 :: digits else digits

/-- Every index at which `pat` occurs in `s`, left to right. -/
def allIndexOf (s pat : List Nat) : List Nat :=
  (List.range (s.length + 1)).filter fun i => (s.drop i).take pat.length == pat && i + pat.length ≤ s.length

/-- `strings.Split` on bytes: Go's semantics for an empty separator (every
byte) and for an empty string (one empty field). -/
partial def splitBytes (s sep : List Nat) : List (List Nat) :=
  if sep.isEmpty then s.map fun b => [b]
  else
    match listIndexOf s sep with
    | some i => s.take i :: splitBytes (s.drop (i + sep.length)) sep
    | none => [s]

/-- `strings.Fields` on bytes: runs of non-space bytes. -/
partial def fieldsBytes (s : List Nat) : List (List Nat) :=
  let s := s.dropWhile isSpaceByte
  if s.isEmpty then []
  else
    let w := s.takeWhile fun b => !isSpaceByte b
    w :: fieldsBytes (s.drop w.length)

/-- Replace every non-overlapping occurrence of `old` by `new`. -/
partial def replaceBytes (s old new : List Nat) : List Nat :=
  if old.isEmpty then s
  else
    match listIndexOf s old with
    | some i => s.take i ++ new ++ replaceBytes (s.drop (i + old.length)) old new
    | none => s

def lowerByte (b : Nat) : Nat := if 65 ≤ b && b ≤ 90 then b + 32 else b
def upperByte (b : Nat) : Nat := if 97 ≤ b && b ≤ 122 then b - 32 else b

/-- The bytes of each string in a slice of strings on the heap. -/
def strSliceBytes (heap : Heap Val) : Val → Option (List (List Nat))
  | .slice _ _ 0#64 _ => some []
  | .slice ref off len _ =>
    match heap[ref]? with
    | some (.arr es) => ((es.drop off.toNat).take len.toNat).mapM (strBytes heap)
    | _ => none
  | _ => none

/-- The elements of a heap-backed slice (nil is empty). -/
def sliceElems (heap : Heap Val) : Val → Option (List Val)
  | .slice 0 _ _ _ => some []
  | .slice ref off len _ =>
    match heap[ref]? with
    | some (.arr es) => some ((es.drop off.toNat).take len.toNat)
    | _ => none
  | _ => none

/-- Key equality for maps: strings by their bytes, the rest by `eqVal`. -/
def keyEq (heap : Heap Val) (a b : Val) : Bool :=
  match a, b with
  | .str .., .str .. => strBytes heap a == strBytes heap b
  | _, _ => eqVal a b == some true

/-- The entries of the map object `m` (`.boxed 1 p`). -/
def mapEntries (heap : Heap Val) : Val → Option (Val × List (Val × Val))
  | .boxed 1 p =>
    match heapLoad heap p with
    | .ok (.arr es) => some (p, es.filterMap fun | .tuple [k, v] => some (k, v) | _ => none)
    | _ => none
  | _ => none

def mapObj (entries : List (Val × Val)) : Val :=
  .arr (entries.map fun (k, v) => .tuple [k, v])

/-- Overwrite `es` from index `i` with `vs`. -/
def writeAt (es : List Val) (i : Nat) (vs : List Val) : List Val :=
  es.take i ++ vs ++ es.drop (i + vs.length)

/-- Decode one UTF-8 sequence at `i`: the code point and its width. Invalid
or truncated input gives U+FFFD of width 1, as Go's range loop does. -/
def decodeRune (bs : List Nat) (i : Nat) : Nat × Nat :=
  let cont (b : Option Nat) : Bool := match b with | some b => 0x80 ≤ b && b < 0xC0 | none => false
  let low (b : Option Nat) : Nat := (b.getD 0) % 0x40
  match bs[i]? with
  | none => (0xFFFD, 1)
  | some b0 =>
    if b0 < 0x80 then (b0, 1)
    else if b0 < 0xC2 then (0xFFFD, 1)
    else if b0 < 0xE0 then
      if cont bs[i+1]? then (((b0 % 0x20) <<< 6) ||| low bs[i+1]?, 2) else (0xFFFD, 1)
    else if b0 < 0xF0 then
      if cont bs[i+1]? && cont bs[i+2]? then
        let r := ((b0 % 0x10) <<< 12) ||| (low bs[i+1]? <<< 6) ||| low bs[i+2]?
        if r < 0x800 || (0xD800 ≤ r && r ≤ 0xDFFF) then (0xFFFD, 1) else (r, 3)
      else (0xFFFD, 1)
    else if b0 < 0xF5 then
      if cont bs[i+1]? && cont bs[i+2]? && cont bs[i+3]? then
        let r := ((b0 % 0x08) <<< 18) ||| (low bs[i+1]? <<< 12) ||| (low bs[i+2]? <<< 6) ||| low bs[i+3]?
        if r < 0x10000 || r > 0x10FFFF then (0xFFFD, 1) else (r, 4)
      else (0xFFFD, 1)
    else (0xFFFD, 1)

/-- UTF-8 encode one code point; surrogates and values above U+10FFFF
encode as U+FFFD, as Go's `string(r)` does. -/
def encodeRune (r : Nat) : List Nat :=
  let r := if r > 0x10FFFF || (0xD800 ≤ r && r ≤ 0xDFFF) then 0xFFFD else r
  if r < 0x80 then [r]
  else if r < 0x800 then [0xC0 ||| (r >>> 6), 0x80 ||| (r % 0x40)]
  else if r < 0x10000 then [0xE0 ||| (r >>> 12), 0x80 ||| ((r >>> 6) % 0x40), 0x80 ||| (r % 0x40)]
  else [0xF0 ||| (r >>> 18), 0x80 ||| ((r >>> 12) % 0x40), 0x80 ||| ((r >>> 6) % 0x40), 0x80 ||| (r % 0x40)]

/-- All code points of a byte string. -/
partial def decodeAll (bs : List Nat) (i : Nat := 0) : List Nat :=
  if i < bs.length then
    let (r, n) := decodeRune bs i
    r :: decodeAll bs (i + n)
  else []

/-- Go index semantics: a negative index is out of range, so compare unsigned
after sign-extending to 64 bits. -/
def asIndex : Val → Option (BitVec 64)
  | .int _ i => some (i.signExtend 64)
  | _ => none

/-- Pure instructions: no heap access. -/
def denotePure (or : Oracle) (heap : Heap Val) : Op → List Val → Res Val
  | .const v, [] => .ok v
  | .box tag, [v] => .ok (.boxed tag v)
  | .isBoxed, [x] => .ok (.bool (match x with | .boxed .. => true | _ => false))
  | .unbox tag commaOk zero, [.boxed t v] =>
    if t == tag then .ok (if commaOk then .tuple [v, .bool true] else v)
    else if commaOk then .ok (.tuple [zero, .bool false])
    else .panic .typeAssertion
  | .unbox _ commaOk zero, [.opaqueNil] =>
    if commaOk then .ok (.tuple [zero, .bool false]) else .panic .typeAssertion
  | .nilKnown, [x] => .ok (.bool (match x with | .boxed .. | .opaqueNil => true | _ => false))
  | .isNilOpaque, [x] => .ok (.bool (match x with | .opaqueNil => true | _ => false))
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
  -- Shift counts at or beyond the width give 0 (or the sign fill) without
  -- computing a huge shift: Go's semantics, and Lean would otherwise try to
  -- build 2^k.
  | .shl cs, [.int w a, n] =>
    match shiftCount cs n with
    | .ok k => .ok (.int w (if k ≥ w then 0#w else a <<< k))
    | .panic p => .panic p
    | .outOfFuel => .outOfFuel
  | .shr signed cs, [.int w a, n] =>
    match shiftCount cs n with
    | .ok k =>
      if k ≥ w then .ok (.int w (if signed && a.msb then -1#w else 0#w))
      else .ok (.int w (if signed then a.sshiftRight k else a >>> k))
    | .panic p => .panic p
    | .outOfFuel => .outOfFuel
  | .eq, [a@(.str ..), b@(.str ..)] =>
    match strBytes heap a, strBytes heap b with
    | some x, some y => .ok (.bool (x == y))
    | _, _ => .panic .modelStuck
  | .ne, [a@(.str ..), b@(.str ..)] =>
    match strBytes heap a, strBytes heap b with
    | some x, some y => .ok (.bool (x != y))
    | _, _ => .panic .modelStuck
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
  | .indexAddr, [.ptr 0 _, _] => .panic .nilDereference
  | .indexAddr, [.ptr ref path, i] =>
    -- `&a[i]` for a pointer to an array: bounds come from the array object.
    match asIndex i, heap[ref]? >>= (getPath · path) with
    | some i, some (.arr es) =>
      if i.toNat < es.length then .ok (.ptr ref (path ++ [i.toNat])) else .panic .indexOutOfRange
    | _, _ => .panic .modelStuck
  | .arrIndex, [.arr es, i] =>
    match asIndex i with
    | some i =>
      match es[i.toNat]? with
      | some v => .ok v
      | none => .panic .indexOutOfRange
    | none => .panic .modelStuck
  | .reslice, [.ptr 0 _, _, _] => .panic .nilDereference
  | .reslice, [.ptr ref path, lo, hi] =>
    -- `a[lo:hi]` for a pointer to an array: the slice aliases the array.
    match asIndex lo, asIndex hi, heap[ref]? >>= (getPath · path) with
    | some lo, some hi, some (.arr es) =>
      if path.isEmpty && lo.ule hi && hi.toNat ≤ es.length then
        .ok (.slice ref lo (hi - lo) (BitVec.ofNat 64 es.length - lo))
      else if !path.isEmpty then .panic .modelStuck
      else .panic .sliceBounds
    | _, _, _ => .panic .modelStuck
  | .strLen, [.ptr ref path] =>
    -- `len(a)` for a pointer to an array, reached through the generic path.
    match heap[ref]? >>= (getPath · path) with
    | some (.arr es) => .ok (.int 64 (BitVec.ofNat 64 es.length))
    | _ => .panic .modelStuck
  | .isNil, [.ptr r _] => .ok (.bool (r == 0))
  | .isNil, [.err nonNil] => .ok (.bool !nonNil)
  | .strLen, [.str _ _ len] => .ok (.int 64 len)
  | .strIndex, [s@(.str _ _ len), i] =>
    match asIndex i, strBytes heap s with
    | some i, some bs =>
      if i.ult len then
        match bs[i.toNat]? with
        | some b => .ok (.int 8 (BitVec.ofNat 8 b))
        | none => .panic .modelStuck
      else .panic .indexOutOfRange
    | _, _ => .panic .modelStuck
  | .strSlice, [.str ref off len, lo, hi] =>
    match asIndex lo, asIndex hi with
    | some lo, some hi =>
      if lo.ule hi && hi.ule len then .ok (.str ref (off + lo) (hi - lo)) else .panic .sliceBounds
    | _, _ => .panic .modelStuck
  | .strHasPrefix, [s, p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y => .ok (.bool (x.take y.length == y))
    | _, _ => .panic .modelStuck
  | .strHasSuffix, [s, p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y => .ok (.bool (x.drop (x.length - y.length) == y && y.length ≤ x.length))
    | _, _ => .panic .modelStuck
  | .strContains, [s, p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y => .ok (.bool (listIndexOf x y).isSome)
    | _, _ => .panic .modelStuck
  | .strIndexOf, [s, p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y => .ok (.int 64 (match listIndexOf x y with | some i => BitVec.ofNat 64 i | none => -1#64))
    | _, _ => .panic .modelStuck
  | .strTrimPrefix, [s@(.str ref off len), p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y =>
      if x.take y.length == y && y.length ≤ x.length then
        .ok (.str ref (off + BitVec.ofNat 64 y.length) (len - BitVec.ofNat 64 y.length))
      else .ok s
    | _, _ => .panic .modelStuck
  | .strTrimSuffix, [s@(.str ref off len), p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y =>
      if y.length ≤ x.length && x.drop (x.length - y.length) == y then
        .ok (.str ref off (len - BitVec.ofNat 64 y.length))
      else .ok s
    | _, _ => .panic .modelStuck
  | .strTrimSpace, [s@(.str ref off len)] =>
    match strBytes heap s with
    | some x =>
      let lead := (x.takeWhile isSpaceByte).length
      let trail := (x.reverse.takeWhile isSpaceByte).length
      if lead + trail ≥ x.length then .ok (.str 0 0#64 0#64)
      else .ok (.str ref (off + BitVec.ofNat 64 lead) (len - BitVec.ofNat 64 (lead + trail)))
    | none => .panic .modelStuck
  | .strAtoi, [s] =>
    match strBytes heap s with
    | some x =>
      match atoiBytes x with
      | some v => .ok (.tuple [.int 64 (BitVec.ofInt 64 v), .err false])
      | none => .ok (.tuple [.int 64 0#64, .err true])
    | none => .panic .modelStuck
  | .strIndexByte, [s, .int _ b] =>
    match strBytes heap s with
    | some x => .ok (.int 64 (match listIndexOf x [b.toNat % 256] with | some i => BitVec.ofNat 64 i | none => -1#64))
    | none => .panic .modelStuck
  | .strLastIndex, [s, p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y => .ok (.int 64 (match (allIndexOf x y).getLast? with | some i => BitVec.ofNat 64 i | none => -1#64))
    | _, _ => .panic .modelStuck
  | .strCount, [s, p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y =>
      if y.isEmpty then .ok (.int 64 (BitVec.ofNat 64 (x.length + 1)))
      else .ok (.int 64 (BitVec.ofNat 64 (splitBytes x y).length.pred))
    | _, _ => .panic .modelStuck
  | .strEqualFold, [s, p] =>
    match strBytes heap s, strBytes heap p with
    | some x, some y => .ok (.bool (x.map lowerByte == y.map lowerByte))
    | _, _ => .panic .modelStuck
  | .strTrimLeft, [s@(.str ref off len), c] =>
    match strBytes heap s, strBytes heap c with
    | some x, some cut =>
      let n := (x.takeWhile (cut.contains ·)).length
      .ok (.str ref (off + BitVec.ofNat 64 n) (len - BitVec.ofNat 64 n))
    | _, _ => .panic .modelStuck
  | .strTrimRight, [s@(.str ref off len), c] =>
    match strBytes heap s, strBytes heap c with
    | some x, some cut =>
      let n := (x.reverse.takeWhile (cut.contains ·)).length
      .ok (.str ref off (len - BitVec.ofNat 64 n))
    | _, _ => .panic .modelStuck
  | .strTrim, [s@(.str ref off len), c] =>
    match strBytes heap s, strBytes heap c with
    | some x, some cut =>
      let l := (x.takeWhile (cut.contains ·)).length
      let r := (x.reverse.takeWhile (cut.contains ·)).length
      if l + r ≥ x.length then .ok (.str 0 0#64 0#64)
      else .ok (.str ref (off + BitVec.ofNat 64 l) (len - BitVec.ofNat 64 (l + r)))
    | _, _ => .panic .modelStuck
  | .mkTuple, vs => .ok (.tuple vs)
  | .extract i, [.tuple vs] =>
    match vs[i]? with
    | some v => .ok v
    | none => .panic .modelStuck
  | .reslice3, [.slice ref off _ cap, lo, hi, mx] =>
    match asIndex lo, asIndex hi, asIndex mx with
    | some lo, some hi, some mx =>
      if lo.ule hi && hi.ule mx && mx.ule cap then .ok (.slice ref (off + lo) (hi - lo) (mx - lo))
      else .panic .sliceBounds
    | _, _, _ => .panic .modelStuck
  | .reslice3, [.ptr 0 _, _, _, _] => .panic .nilDereference
  | .reslice3, [.ptr ref path, lo, hi, mx] =>
    match asIndex lo, asIndex hi, asIndex mx, heap[ref]? >>= (getPath · path) with
    | some lo, some hi, some mx, some (.arr es) =>
      if path.isEmpty && lo.ule hi && hi.ule mx && mx.toNat ≤ es.length then
        .ok (.slice ref lo (hi - lo) (mx - lo))
      else .panic (if path.isEmpty then .sliceBounds else .modelStuck)
    | _, _, _, _ => .panic .modelStuck
  | .reslice, [.slice ref off _ cap, lo, hi] =>
    match asIndex lo, asIndex hi with
    | some lo, some hi =>
      if lo.ule hi && hi.ule cap then .ok (.slice ref (off + lo) (hi - lo) (cap - lo))
      else .panic .sliceBounds
    | _, _ => .panic .modelStuck
  | _, _ => .panic .modelStuck

/-- Allocate each string, then a slice holding them. -/
def allocStrSlice (heap : Heap Val) (parts : List (List Nat)) : Val × Heap Val :=
  let (vals, heap) := parts.foldl (fun (acc, heap) bs =>
    let (v, heap) := strAlloc heap bs
    (acc ++ [v], heap)) ([], heap)
  let (p, heap) := heapAlloc heap (.arr vals)
  match p with
  | .ptr ref _ => (.slice ref 0#64 (BitVec.ofNat 64 vals.length) (BitVec.ofNat 64 vals.length), heap)
  | _ => (.unit, heap)

/-- All instructions: heap-touching ones here, the rest via `denotePure`. -/
def denote (or : Oracle) (heap : Heap Val) : Op → List Val → Res (Val × Heap Val)
  | .alloc zero, [] => .ok (heapAlloc heap zero)
  | .makeMap, [] =>
    let (p, heap) := heapAlloc heap (.arr [])
    .ok (.boxed 1 p, heap)
  | .mapLookup commaOk zero, [m, k] =>
    let found : Option Val := match m with
      | .opaqueNil => some zero
      | _ => (mapEntries heap m).map fun (_, es) =>
        ((es.find? fun (k', _) => keyEq heap k k').map (·.2)).getD zero
    let hit : Bool := match mapEntries heap m with
      | some (_, es) => es.any fun (k', _) => keyEq heap k k'
      | none => false
    match found with
    | some v => .ok (if commaOk then .tuple [v, .bool hit] else v, heap)
    | none => .panic .modelStuck
  | .mapUpdate, [.opaqueNil, _, _] => .panic .nilMapWrite
  | .mapUpdate, [m, k, v] =>
    match mapEntries heap m with
    | some (p, es) =>
      let es := if es.any (fun (k', _) => keyEq heap k k') then
          es.map fun (k', v') => if keyEq heap k k' then (k', v) else (k', v')
        else es ++ [(k, v)]
      match heapStore heap p (mapObj es) with
      | .ok heap => .ok (.unit, heap)
      | .panic k => .panic k
      | .outOfFuel => .outOfFuel
    | none => .panic .modelStuck
  | .mapDelete, [.opaqueNil, _] => .ok (.unit, heap)
  | .mapDelete, [m, k] =>
    match mapEntries heap m with
    | some (p, es) =>
      match heapStore heap p (mapObj (es.filter fun (k', _) => !keyEq heap k k')) with
      | .ok heap => .ok (.unit, heap)
      | .panic k => .panic k
      | .outOfFuel => .outOfFuel
    | none => .panic .modelStuck
  | .mapLen, [.opaqueNil] => .ok (.int 64 0#64, heap)
  | .mapLen, [m] =>
    match mapEntries heap m with
    | some (_, es) => .ok (.int 64 (BitVec.ofNat 64 es.length), heap)
    | none => .panic .modelStuck
  | .mapRange, [m] =>
    let (p, heap) := heapAlloc heap (.tuple [m, .int 64 0#64])
    .ok (p, heap)
  | .mapNext zk zv, [it] =>
    match heapLoad heap it with
    | .ok (.tuple [m, .int 64 i]) =>
      match mapEntries heap m with
      | some (_, es) =>
        match es[i.toNat]? with
        | some (k, v) =>
          match heapStore heap it (.tuple [m, .int 64 (i + 1)]) with
          | .ok heap => .ok (.tuple [.bool true, k, v], heap)
          | .panic k => .panic k
          | .outOfFuel => .outOfFuel
        | none => .ok (.tuple [.bool false, zk, zv], heap)
      -- A nil map, or one the model knows nothing about: no entries.
      | none => .ok (.tuple [.bool false, zk, zv], heap)
    | _ => .panic .modelStuck
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
  | .strConst bs, [] => .ok (strAlloc heap bs)
  | .strConcat, [a, b] =>
    match strBytes heap a, strBytes heap b with
    | some x, some y => .ok (strAlloc heap (x ++ y))
    | _, _ => .panic .modelStuck
  | .bytesToStr, [.slice ref off len _] =>
    match strBytes heap (.str ref off len) with
    | some x => .ok (strAlloc heap x)
    | none => .panic .modelStuck
  | .strToBytes, [s] =>
    match strBytes heap s with
    | some x =>
      let (p, heap) := heapAlloc heap (.arr (x.map fun b => .int 8 (BitVec.ofNat 8 b)))
      match p with
      | .ptr ref _ => .ok (.slice ref 0#64 (BitVec.ofNat 64 x.length) (BitVec.ofNat 64 x.length), heap)
      | _ => .panic .modelStuck
    | none => .panic .modelStuck
  | .strToRunes, [s] =>
    match strBytes heap s with
    | some x =>
      let rs := decodeAll x
      let (p, heap) := heapAlloc heap (.arr (rs.map fun r => .int 32 (BitVec.ofNat 32 r)))
      match p with
      | .ptr ref _ => .ok (.slice ref 0#64 (BitVec.ofNat 64 rs.length) (BitVec.ofNat 64 rs.length), heap)
      | _ => .panic .modelStuck
    | none => .panic .modelStuck
  | .runesToStr, [rs] =>
    match sliceElems heap rs with
    | some es =>
      match es.mapM (fun | .int _ v => some v.toNat | _ => none) with
      | some rs => .ok (strAlloc heap (rs.flatMap encodeRune))
      | none => .panic .modelStuck
    | none => .panic .modelStuck
  | .runeToStr, [.int _ v] => .ok (strAlloc heap (encodeRune v.toNat))
  | .strNext, [p] =>
    match heapLoad heap p with
    | .ok (.tuple [s, .int _ pos]) =>
      match strBytes heap s with
      | some bs =>
        let i := pos.toNat
        if i < bs.length then
          let (r, n) := decodeRune bs i
          match heapStore heap p (.tuple [s, .int 64 (BitVec.ofNat 64 (i + n))]) with
          | .ok heap => .ok (.tuple [.bool true, .int 64 (BitVec.ofNat 64 i), .int 32 (BitVec.ofNat 32 r)], heap)
          | _ => .panic .modelStuck
        else .ok (.tuple [.bool false, .int 64 0#64, .int 32 0#32], heap)
      | none => .panic .modelStuck
    | _ => .panic .modelStuck
  | .strItoa, [.int _ v] => .ok (strAlloc heap (itoaBytes v.toInt))
  | .strToLower, [s] =>
    match strBytes heap s with
    | some x => .ok (strAlloc heap (x.map lowerByte))
    | none => .panic .modelStuck
  | .strToUpper, [s] =>
    match strBytes heap s with
    | some x => .ok (strAlloc heap (x.map upperByte))
    | none => .panic .modelStuck
  | .strReplaceAll, [s, o, n] =>
    match strBytes heap s, strBytes heap o, strBytes heap n with
    | some x, some old, some new => .ok (strAlloc heap (replaceBytes x old new))
    | _, _, _ => .panic .modelStuck
  | .errNew, _ => .ok (.err true, heap)
  | .sbString, [p] =>
    match heapLoad heap p with
    | .ok s@(.str ..) => .ok (s, heap)
    | .ok _ => .ok (.str 0 0#64 0#64, heap) -- a zero Builder cell holds the empty string
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  | .sbLen, [p] =>
    match heapLoad heap p with
    | .ok (.str _ _ len) => .ok (.int 64 len, heap)
    | .ok _ => .ok (.int 64 0#64, heap)
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  | .sbReset, [p] =>
    match heapStore heap p (.str 0 0#64 0#64) with
    | .ok heap => .ok (.unit, heap)
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  | .sbWriteString, [p, s] =>
    match heapLoad heap p, strBytes heap s with
    | .ok cur, some bs =>
      let old := match strBytes heap cur with | some x => x | none => []
      let (v, heap) := strAlloc heap (old ++ bs)
      match heapStore heap p v with
      | .ok heap => .ok (.tuple [.int 64 (BitVec.ofNat 64 bs.length), .err false], heap)
      | .panic k => .panic k
      | .outOfFuel => .outOfFuel
    | .panic k, _ => .panic k
    | _, _ => .panic .modelStuck
  | .sbWriteByte, [p, .int _ c] =>
    match heapLoad heap p with
    | .ok cur =>
      let old := match strBytes heap cur with | some x => x | none => []
      let (v, heap) := strAlloc heap (old ++ [c.toNat % 256])
      match heapStore heap p v with
      | .ok heap => .ok (.err false, heap)
      | .panic k => .panic k
      | .outOfFuel => .outOfFuel
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  | .sbWriteRune, [p, .int _ r] =>
    match heapLoad heap p with
    | .ok cur =>
      let old := match strBytes heap cur with | some x => x | none => []
      let enc := encodeRune r.toNat
      let (v, heap) := strAlloc heap (old ++ enc)
      match heapStore heap p v with
      | .ok heap => .ok (.tuple [.int 64 (BitVec.ofNat 64 enc.length), .err false], heap)
      | .panic k => .panic k
      | .outOfFuel => .outOfFuel
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  | .strRepeat, [s, .int _ n] =>
    if n.msb then .panic .explicit -- strings: negative Repeat count
    else
      match strBytes heap s with
      | some x =>
        if n.toNat * x.length > 4096 then .panic .modelStuck
        else .ok (strAlloc heap ((List.replicate n.toNat x).flatten))
      | none => .panic .modelStuck
  | .strSplit, [s, sep] =>
    match strBytes heap s, strBytes heap sep with
    | some x, some y => .ok (allocStrSlice heap (splitBytes x y))
    | _, _ => .panic .modelStuck
  | .strFields, [s] =>
    match strBytes heap s with
    | some x => .ok (allocStrSlice heap (fieldsBytes x))
    | none => .panic .modelStuck
  | .strJoin, [elems, sep] =>
    match strSliceBytes heap elems, strBytes heap sep with
    | some xs, some y => .ok (strAlloc heap (xs.intersperse y).flatten)
    | _, _ => .panic .modelStuck
  | .appendSlice zero, [s, t] =>
    match sliceElems heap t with
    | none => .panic .modelStuck
    | some [] => .ok (s, heap) -- append(s) is s, nil included
    | some ts =>
      match s with
      | .slice 0 _ _ _ =>
        let (p, heap) := heapAlloc heap (.arr ts)
        match p with
        | .ptr ref _ => .ok (.slice ref 0#64 (BitVec.ofNat 64 ts.length) (BitVec.ofNat 64 ts.length), heap)
        | _ => .panic .modelStuck
      | .slice ref off len cap =>
        match heap[ref]? with
        | some (.arr es) =>
          let newLen := len.toNat + ts.length
          if newLen ≤ cap.toNat then
            -- Room in the backing array: write in place (aliases stay live).
            .ok (.slice ref off (BitVec.ofNat 64 newLen) cap,
                 heap.set! ref (.arr (writeAt es (off.toNat + len.toNat) ts)))
          else
            -- Grow: Go doubles small slices; the exact capacity is
            -- implementation-defined and only visible through `cap`.
            let newCap := max (2 * cap.toNat) newLen
            if newCap > 1024 then .panic .modelStuck
            else
              let contents := (es.drop off.toNat).take len.toNat ++ ts ++ List.replicate (newCap - newLen) zero
              let (p, heap) := heapAlloc heap (.arr contents)
              match p with
              | .ptr ref' _ => .ok (.slice ref' 0#64 (BitVec.ofNat 64 newLen) (BitVec.ofNat 64 newCap), heap)
              | _ => .panic .modelStuck
        | _ => .panic .modelStuck
      | _ => .panic .modelStuck
  | .copySlice, [d, t] =>
    match sliceElems heap t, d with
    | some ts, .slice 0 _ _ _ => .ok (.int 64 (BitVec.ofNat 64 (min 0 ts.length)), heap)
    | some ts, .slice ref off len _ =>
      let n := min len.toNat ts.length
      match heap[ref]? with
      | some (.arr es) =>
        .ok (.int 64 (BitVec.ofNat 64 n), heap.set! ref (.arr (writeAt es off.toNat (ts.take n))))
      | _ => .panic .modelStuck
    | _, _ => .panic .modelStuck
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
