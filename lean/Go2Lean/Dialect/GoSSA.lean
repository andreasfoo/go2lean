import Go2Lean.SSA.Core
import Lean.Data.Json

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

/-- A struct field as `encoding/json` sees it: its key, its position in the
struct value, its type in the table, and its tag options. -/
structure JField where
  key : List Nat
  idx : Nat
  ty : Nat
  omitEmpty : Bool := false
  omitZero : Bool := false
  /-- An embedded struct or an SDK `json:",inline"` field: its members are
  the struct's own. -/
  inline : Bool := false
  deriving Repr

/-- The JSON metadata an OpenAI/Anthropic SDK response type keeps (its
`JSON` field): decoding sets the raw text, one `respjson.Field` per typed
member (status and raw), and `ExtraFields` for the others. -/
structure JMeta where
  /-- Index of the `JSON` field in the struct. -/
  field : Nat
  /-- Inside it: the index of `raw`, of `ExtraFields` (if any), and of the
  `respjson.Field` for each typed member, by key. -/
  raw : Nat
  extra : Option Nat
  members : List (List Nat × Nat)
  deriving Repr

/-- How `encoding/json` (and the SDKs' encoders) treat a Go type. Table
entries refer to each other by index. -/
inductive JDesc where
  /-- A string; `dflt` is what a zero value encodes as (the SDKs' constant
  types, `default:"text"`), empty for none. -/
  | str (dflt : List Nat)
  | bool
  | int (w : Nat) (signed : Bool)
  /-- floats: decoded as opaque numbers; encoding gets stuck. -/
  | float
  /-- `[]byte`: base64. -/
  | bytes
  /-- `json.RawMessage`: the bytes themselves. -/
  | raw
  | slice (elem : Nat)
  | arr (n elem : Nat)
  | ptr (elem : Nat)
  /-- A map with string or integer keys; keys are encoded sorted. -/
  | map (key val : Nat)
  /-- `interface{}`: encoded by the dynamic type (`JTable.boxed`), decoded
  as strings, bools, numbers, maps and slices. -/
  | iface
  /-- A struct; `meta` for SDK response types. `api`: the SDKs' own
  decoder (apijson) reads it: exact keys, null leaves a member unchanged,
  members that do not decode are skipped (the model gets stuck there). -/
  | strct (fields : List JField) (md : Option JMeta) (api : Bool)
  /-- An SDK param union: exactly one variant field present is encoded. -/
  | union (variants : List JField)
  /-- `param.Opt[T]` (`Value`, `status`): omitted, null or the value. -/
  | opt (elem : Nat)
  /-- Anything else: the model gets stuck. -/
  | opaque
  deriving Repr

/-- The JSON descriptions of a program's types, the descriptions of the
dynamic types interfaces hold (by type tag), and the type tags of
string, bool, float64, `map[string]interface{}` and `[]interface{}`. -/
structure JTable where
  descs : Array JDesc
  /-- The zero value of each entry's type (what decoding starts from). -/
  zeros : Array Val
  boxed : List (Nat × Nat) := []
  basic : List Nat := []
  deriving Repr

/-- A zero value in the frontend's zero-model JSON (`{"k": "int", "w": 64}`,
`{"k": "struct", "fields": [...]}`, ...). -/
partial def zeroOfJson (j : Lean.Json) : Val :=
  match (j.getObjValAs? String "k").toOption with
  | some "int" => .int ((j.getObjValAs? Nat "w").toOption.getD 64) 0
  | some "bool" => .bool false
  | some "err" => .err false
  | some "str" => .str 0 0#64 0#64
  | some "nil" => .opaqueNil
  | some "ptr" => .ptr 0 []
  | some "slice" => .slice 0 0#64 0#64 0#64
  | some "struct" => .structv (((j.getObjValAs? (Array Lean.Json) "fields").toOption.getD #[]).toList.map zeroOfJson)
  | some "arr" => .arr (((j.getObjValAs? (Array Lean.Json) "elems").toOption.getD #[]).toList.map zeroOfJson)
  | _ => .unit

def jBytesOf (j : Lean.Json) (key : String) : List Nat :=
  ((j.getObjValAs? String key).toOption.getD "").toUTF8.toList.map (·.toNat)

def jNatOf (j : Lean.Json) (key : String) : Nat := (j.getObjValAs? Nat key).toOption.getD 0

def jFieldOfJson (j : Lean.Json) : JField :=
  { key := jBytesOf j "key", idx := jNatOf j "idx", ty := jNatOf j "ty",
    omitEmpty := (j.getObjValAs? Bool "oe").toOption.getD false,
    omitZero := (j.getObjValAs? Bool "oz").toOption.getD false,
    inline := (j.getObjValAs? Bool "in").toOption.getD false }

def jDescOfJson (j : Lean.Json) : JDesc :=
  let fields (key : String) : List JField :=
    ((j.getObjValAs? (Array Lean.Json) key).toOption.getD #[]).toList.map jFieldOfJson
  match (j.getObjValAs? String "k").toOption with
  | some "str" => .str (jBytesOf j "dflt")
  | some "bool" => .bool
  | some "int" => .int (jNatOf j "w") ((j.getObjValAs? Bool "s").toOption.getD true)
  | some "float" => .float
  | some "bytes" => .bytes
  | some "raw" => .raw
  | some "slice" => .slice (jNatOf j "e")
  | some "arr" => .arr (jNatOf j "n") (jNatOf j "e")
  | some "ptr" => .ptr (jNatOf j "e")
  | some "map" => .map (jNatOf j "key") (jNatOf j "val")
  | some "iface" => .iface
  | some "strct" =>
    let md : Option JMeta := (j.getObjVal? "meta").toOption.bind fun m =>
      if m matches .null then none else
      some { field := jNatOf m "field", raw := jNatOf m "raw",
             extra := (m.getObjValAs? Nat "extra").toOption,
             members := ((m.getObjValAs? (Array Lean.Json) "members").toOption.getD #[]).toList.filterMap fun x =>
               match x with
               | .arr #[.str k, .num n] => some (k.toUTF8.toList.map (·.toNat), n.mantissa.toNat)
               | _ => none }
    .strct (fields "fields") md ((j.getObjValAs? Bool "api").toOption.getD false)
  | some "union" => .union (fields "variants")
  | some "opt" => .opt (jNatOf j "e")
  | _ => .opaque

/-- The table from the frontend's JSON (a string in the generated module:
a literal of thousands of descriptions is too slow to elaborate). -/
def JTable.ofJsonString (s : String) : JTable :=
  match Lean.Json.parse s with
  | .ok j =>
    { descs := ((j.getObjValAs? (Array Lean.Json) "descs").toOption.getD #[]).map jDescOfJson,
      zeros := ((j.getObjValAs? (Array Lean.Json) "zeros").toOption.getD #[]).map zeroOfJson,
      boxed := ((j.getObjValAs? (Array Lean.Json) "boxed").toOption.getD #[]).toList.filterMap fun x =>
        match x with
        | .arr #[.num t, .num d] => some (t.mantissa.toNat, d.mantissa.toNat)
        | _ => none,
      basic := ((j.getObjValAs? (Array Nat) "basic").toOption.getD #[]).toList }
  | .error _ => { descs := #[], zeros := #[] }

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
  /-- `==` / `!=` on arrays and structs that hold strings: strings compare
  by their bytes, the rest as `eq`. -/
  | eqDeep | neDeep
  /-- `hex.EncodeToString(b)`: two lowercase hex digits per byte. -/
  | hexEncode
  /-- `fmt.Sprintf(format, args...)` for the verbs `%s %d %v %t %%` without
  flags or widths on strings, integers and bools the program boxed; any
  other format or value gets the model stuck (the call is then unmodelled). -/
  | sprintf
  /-- Can `sprintf` format these arguments? -/
  | sprintfOk
  /-- `reflect.ValueOf(x)`: the interface value itself. -/
  | reflectOf
  /-- `reflect.ValueOf(x).IsZero()` on a value the program boxed. -/
  | reflectIsZero
  /-- An array of `n` bytes distinct from every other `freshArr` value of
  the run (a random UUID): the bytes spell the heap size, which the op
  grows by one. -/
  | freshArr (n : Nat)
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
  /-- `x.(I)` for an interface `I`: the boxed types of the program that
  implement `I` have tags `tags`; the value stays as it is. -/
  | assertIface (tags : List Nat) (commaOk : Bool)
  /-- The method a call on interface value `x` runs, args `[x, f₁, …, fₙ]`:
  `fᵢ` is the method of the boxed type with tag `tags[i]`; the result calls
  it with the boxed value as receiver. A nil interface gives a nil func
  (the call panics), any other value an unknown one. -/
  | dispatch (tags : List Nat)
  /-- A func value naming function `f` of the program, capturing the
  arguments (a closure's free variables). -/
  | funcRef (f : Nat)
  /-- A func value naming function `f` of the model registered as `pkg`;
  the driver links it into a `funcRef`, else it is an unknown non-nil func. -/
  | xfuncRef (pkg : String) (f : Nat)
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
  /-- `json.Unmarshal(data, p)` for `p` pointing at a `string` (kind 0),
  `bool` (1), `interface{}` (2), `map[string]interface{}` (3) or
  `[]interface{}` (4); args `[data, p]`, yields the error. Decoded values
  are boxed with `tags`, the type tags of string, bool, float64,
  map[string]interface{} and []interface{} (numbers stay opaque). -/
  | jsonUnmarshal (kind : Nat) (tags : List Nat)
  /-- `json.Marshal(v)` for `v` described by entry `root` of `t`; yields
  `(bytes, error)`. -/
  | jsonMarshal (t : JTable) (root : Nat)
  /-- `json.Unmarshal(data, p)` for `*p` described by entry `root` of `t`;
  args `[data, p]`, yields the error. -/
  | jsonDecode (t : JTable) (root : Nat)
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
  -- Arrays and structs compare element by element (the frontend only
  -- lowers == on those whose elements compare without the heap).
  | .arr as, .arr bs => eqVals as bs
  | .structv as, .structv bs => eqVals as bs
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
where
  eqVals : List Val → List Val → Option Bool
    | [], [] => some true
    | a :: as, b :: bs => match eqVal a b, eqVals as bs with
      | some false, _ => some false
      | some true, r => r
      | none, _ => none
    | _, _ => some false



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

/-- The longest string concatenation, a Builder or Join/ReplaceAll may
build (bytes): longer ones get the model stuck. Strings are lists of
bytes and every intermediate string stays on the heap, so a loop
appending to a string would otherwise use memory quadratic in its
length (one search of the sixth full pass was killed at 9 GB). -/
def strLimit : Nat := 65536

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

/-- Equality of arrays and structs whose strings live on the heap. -/
def eqDeep (heap : Heap Val) : Val → Val → Option Bool
  | .str r o l, .str r' o' l' =>
    match strBytes heap (.str r o l), strBytes heap (.str r' o' l') with
    | some x, some y => some (x == y)
    | _, _ => none
  | .arr as, .arr bs => eqDeeps heap as bs
  | .structv as, .structv bs => eqDeeps heap as bs
  | a, b => eqVal a b
where
  eqDeeps (heap : Heap Val) : List Val → List Val → Option Bool
    | [], [] => some true
    | a :: as, b :: bs => match eqDeep heap a b, eqDeeps heap as bs with
      | some false, _ => some false
      | some true, r => r
      | none, _ => none
    | _, _ => some false

/-- Is this the zero value of its type? Strings by their length; `none`
for values the model does not know. -/
def isZeroVal : Val → Option Bool
  | .int _ v => some (v == 0)
  | .bool b => some !b
  | .err b => some !b
  | .ptr r _ => some (r == 0)
  | .slice r .. => some (r == 0)
  | .str _ _ len => some (len == 0)
  | .opaqueNil => some true
  | .boxed .. => some false
  | .arr vs => allZero vs
  | .structv vs => allZero vs
  | _ => none
where
  allZero : List Val → Option Bool
    | [] => some true
    | v :: vs => match isZeroVal v, allZero vs with
      | some false, _ => some false
      | some true, r => r
      | none, _ => none

/-- The bytes `%v` (and `%s`, `%d`, `%t`) prints for a value the program
boxed, or `none`. Integers print signed: the model does not keep the
signedness of a boxed integer, so a large unsigned value would print
wrong; formats of those are rare in the code this models. -/
def fmtArg (heap : Heap Val) (verb : Nat) : Val → Option (List Nat)
  | .boxed _ (.str r o l) => if verb == 115 || verb == 118 then strBytes heap (.str r o l) else none
  | .boxed _ (.int _ v) => if verb == 100 || verb == 118 then some (itoaBytes v.toInt) else none
  | .boxed _ (.bool b) =>
    if verb == 116 || verb == 118 then some (if b then [116, 114, 117, 101] else [102, 97, 108, 115, 101]) else none
  | _ => none

/-- Format `fmt` with `args` (see `Op.sprintf`). -/
def sprintfBytes (heap : Heap Val) : List Nat → List Val → Option (List Nat)
  | [], [] => some []
  | [], _ :: _ => none -- Go appends %!(EXTRA ...): not modelled
  | 37 :: 37 :: rest, args => (37 :: ·) <$> sprintfBytes heap rest args
  | 37 :: verb :: rest, a :: args =>
    match fmtArg heap verb a, sprintfBytes heap rest args with
    | some x, some y => some (x ++ y)
    | _, _ => none
  | 37 :: _, _ => none -- a missing argument or a bare % at the end
  | c :: rest, args => (c :: ·) <$> sprintfBytes heap rest args

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
  | .funcRef f, bound => .ok (.boxed 0 (.tuple (.int 64 (BitVec.ofNat 64 f) :: bound)))
  | .xfuncRef .., _ => .ok (.boxed 0 .unit)
  | .box tag, [v] => .ok (.boxed tag v)
  | .isBoxed, [x] => .ok (.bool (match x with | .boxed .. => true | _ => false))
  | .unbox tag commaOk zero, [.boxed t v] =>
    if t == tag then .ok (if commaOk then .tuple [v, .bool true] else v)
    else if commaOk then .ok (.tuple [zero, .bool false])
    else .panic .typeAssertion
  | .unbox _ commaOk zero, [.opaqueNil] =>
    if commaOk then .ok (.tuple [zero, .bool false]) else .panic .typeAssertion
  | .assertIface tags commaOk, [x] =>
    let fail := if commaOk then .ok (.tuple [.opaqueNil, .bool false]) else .panic .typeAssertion
    match x with
    | .boxed t _ =>
      if tags.contains t then .ok (if commaOk then .tuple [x, .bool true] else x) else fail
    | .opaqueNil => fail
    | _ => .panic .modelStuck
  | .dispatch tags, x :: fs =>
    match x with
    | .boxed t v =>
      match (tags.zip fs).lookup t with
      | some (.boxed 0 (.tuple (f :: bound))) => .ok (.boxed 0 (.tuple (f :: bound ++ [v])))
      | _ => .ok .unit
    | .opaqueNil => .ok .opaqueNil
    | _ => .ok .unit
  | .reflectOf, [x] => .ok x
  | .reflectIsZero, [.boxed _ v] =>
    match isZeroVal v with | some b => .ok (.bool b) | none => .panic .modelStuck
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

/-- `bytes` as a string, invalid UTF-8 sequences replaced by U+FFFD. -/
def utf8Lossy (bytes : List Nat) : String := Id.run do
  let mut out := ""
  let mut i := 0
  for _ in [0:bytes.length] do
    if i ≥ bytes.length then break
    let (r, w) := decodeRune bytes i
    out := out.push (Char.ofNat r)
    i := i + w
  return out

/-- A decoded JSON value as the program would hold it in an interface:
strings, bools, numbers (opaque), `map[string]interface{}` and
`[]interface{}` boxed with their `tags`, null as the nil interface. -/
partial def jsonToVal (tags : List Nat) (heap : Heap Val) : Lean.Json → Val × Heap Val
  | .null => (.opaqueNil, heap)
  | .bool b => (.boxed (tags.getD 1 0) (.bool b), heap)
  | .num _ => (.boxed (tags.getD 2 0) .unit, heap)
  | .str s =>
    let (v, heap) := strAlloc heap (s.toUTF8.toList.map (·.toNat))
    (.boxed (tags.getD 0 0) v, heap)
  | .arr es =>
    let (vs, heap) := es.toList.foldl (fun (acc, heap) e =>
      let (v, heap) := jsonToVal tags heap e
      (acc ++ [v], heap)) ([], heap)
    let (p, heap) := heapAlloc heap (.arr vs)
    let n := BitVec.ofNat 64 vs.length
    match p with
    | .ptr ref _ => (.boxed (tags.getD 4 0) (.slice ref 0#64 n n), heap)
    | _ => (.unit, heap)
  | .obj kvs =>
    let (es, heap) := kvs.toList.foldl (fun (acc, heap) (k, e) =>
      let (kv, heap) := strAlloc heap (k.toUTF8.toList.map (·.toNat))
      let (v, heap) := jsonToVal tags heap e
      (acc ++ [(kv, v)], heap)) ([], heap)
    let (p, heap) := heapAlloc heap (mapObj es)
    (.boxed (tags.getD 3 0) (.boxed 1 p), heap)

/-- `json.Unmarshal` of `bytes` into the `kind` value `p` points at (see
`Op.jsonUnmarshal`): Go's rules for these targets. Invalid JSON is an
error and leaves the target alone; null leaves a string or bool alone and
sets an interface, map or slice to nil; a value of the wrong JSON type is
an error; an object is merged into a non-nil map. -/
def jsonUnmarshalOp (heap : Heap Val) (kind : Nat) (tags : List Nat) (bytes : List Nat) (p : Val) :
    Res (Val × Heap Val) :=
  let store (heap : Heap Val) (v : Val) : Res (Val × Heap Val) :=
    match heapStore heap p v with
    | .ok heap => .ok (.err false, heap)
    | .panic k => .panic k
    | .outOfFuel => .outOfFuel
  let typeErr : Res (Val × Heap Val) := .ok (.err true, heap)
  -- Invalid UTF-8 becomes U+FFFD, as Go's decoder does inside strings
  -- (outside them both parsers reject the document).
  let text := utf8Lossy bytes
  match Lean.Json.parse text with
  | .error _ => .ok (.err true, heap)
  | .ok j =>
    match kind, j with
    | 0, .null | 1, .null => .ok (.err false, heap)
    | 0, .str s => let (v, heap) := strAlloc heap (s.toUTF8.toList.map (·.toNat)); store heap v
    | 0, _ => typeErr
    | 1, .bool b => store heap (.bool b)
    | 1, _ => typeErr
    | 2, j => let (v, heap) := jsonToVal tags heap j; store heap v
    | 3, .null => store heap .opaqueNil
    | 3, .obj _ =>
      match jsonToVal tags heap j, heapLoad heap p with
      | (.boxed _ fresh, heap), .ok .opaqueNil => store heap fresh
      | (.boxed _ fresh, heap), .ok old =>
        -- Merge into the map the target already holds.
        match mapEntries heap old, mapEntries heap fresh with
        | some (q, es), some (_, new) =>
          let es := new.foldl (fun es (k, v) =>
            if es.any (fun (k', _) => keyEq heap k k') then
              es.map fun (k', v') => if keyEq heap k k' then (k', v) else (k', v')
            else es ++ [(k, v)]) es
          match heapStore heap q (mapObj es) with
          | .ok heap => .ok (.err false, heap)
          | .panic k => .panic k
          | .outOfFuel => .outOfFuel
        | _, _ => .panic .modelStuck
      | _, _ => .panic .modelStuck
    | 3, _ => typeErr
    | 4, .null => store heap (.slice 0 0#64 0#64 0#64)
    | 4, .arr _ =>
      match jsonToVal tags heap j with
      | (.boxed _ sl, heap) => store heap sl
      | _ => .panic .modelStuck
    | 4, _ => typeErr
    | _, _ => .panic .modelStuck

-- encoding/json over the type descriptions -------------------------------

def asciiBytes (s : String) : List Nat := s.toUTF8.toList.map (·.toNat)

/-- Go's string encoding: quotes, backslash and control characters
escaped, `<`, `>`, `&`, U+2028 and U+2029 as `\u` escapes (HTML-safe, the
default), invalid UTF-8 as `\ufffd`. -/
def goJsonQuote (bs : List Nat) : List Nat := Id.run do
  let hex (d : Nat) : Nat := if d < 10 then 48 + d else 87 + d
  let u4 (c : Nat) : Array Nat := #[92, 117, hex (c / 4096 % 16), hex (c / 256 % 16), hex (c / 16 % 16), hex (c % 16)]
  let mut out : Array Nat := #[34]
  let mut i := 0
  for _ in [0:bs.length] do
    if i ≥ bs.length then break
    let b := bs[i]!
    if b < 0x80 then
      if b == 34 then out := out ++ #[92, 34]
      else if b == 92 then out := out ++ #[92, 92]
      else if b == 10 then out := out ++ #[92, 110]
      else if b == 13 then out := out ++ #[92, 114]
      else if b == 9 then out := out ++ #[92, 116]
      else if b < 32 || b == 60 || b == 62 || b == 38 then out := out ++ u4 b
      else out := out.push b
      i := i + 1
    else
      let (r, w) := decodeRune bs i
      if r == 0xFFFD && w == 1 then out := out ++ u4 0xFFFD
      else if r == 0x2028 || r == 0x2029 then out := out ++ u4 r
      else out := out ++ ((bs.drop i).take w).toArray
      i := i + w
  return (out.push 34).toList

def bvDec (signed : Bool) (v : BitVec w) : List Nat :=
  asciiBytes (if signed then toString v.toInt else toString v.toNat)

def b64Alphabet : Array Nat :=
  (asciiBytes "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/").toArray

/-- Standard base64 with padding (`[]byte` in JSON). -/
def base64Enc (bs : List Nat) : List Nat :=
  let rec go : List Nat → List Nat
    | a :: b :: c :: rest =>
      let n := a * 65536 + b * 256 + c
      [b64Alphabet[n / 262144 % 64]!, b64Alphabet[n / 4096 % 64]!, b64Alphabet[n / 64 % 64]!, b64Alphabet[n % 64]!] ++ go rest
    | [a, b] =>
      let n := a * 65536 + b * 256
      [b64Alphabet[n / 262144 % 64]!, b64Alphabet[n / 4096 % 64]!, b64Alphabet[n / 64 % 64]!, 61]
    | [a] =>
      let n := a * 65536
      [b64Alphabet[n / 262144 % 64]!, b64Alphabet[n / 4096 % 64]!, 61, 61]
    | [] => []
  go bs

/-- Strict standard base64 decoding; none on invalid input. -/
def base64Dec (bs : List Nat) : Option (List Nat) :=
  let val (c : Nat) : Option Nat := (b64Alphabet.toList.idxOf? c)
  if bs.length % 4 != 0 then none else
  let rec go : List Nat → Option (List Nat)
    | [a, b, 61, 61] => do
      let n := (← val a) * 262144 + (← val b) * 4096
      return [n / 65536 % 256]
    | [a, b, c, 61] => do
      let n := (← val a) * 262144 + (← val b) * 4096 + (← val c) * 64
      return [n / 65536 % 256, n / 256 % 256]
    | a :: b :: c :: d :: rest => do
      let n := (← val a) * 262144 + (← val b) * 4096 + (← val c) * 64 + (← val d)
      return [n / 65536 % 256, n / 256 % 256, n % 256] ++ (← go rest)
    | [] => some []
    | _ => none
  go bs

/-- JSON text without the whitespace outside strings (what Go does to a
`json.RawMessage` it encodes). -/
def compactJson (bs : List Nat) : List Nat := Id.run do
  let mut out : Array Nat := #[]
  let mut inStr := false
  let mut esc := false
  for b in bs do
    if inStr then
      out := out.push b
      if esc then esc := false
      else if b == 92 then esc := true
      else if b == 34 then inStr := false
    else if b == 32 || b == 9 || b == 10 || b == 13 then pure ()
    else
      out := out.push b
      if b == 34 then inStr := true
  return out.toList

/-- Lexicographic order on byte strings (Go sorts map keys this way). -/
def bytesLe (a b : List Nat) : Bool :=
  match a, b with
  | [], _ => true
  | _ :: _, [] => false
  | x :: xs, y :: ys => if x < y then true else if x > y then false else bytesLe xs ys

/-- `omitempty`: false, 0, "", nil, and empty slices and maps. -/
def jIsEmpty (heap : Heap Val) : Val → Bool
  | .bool b => !b
  | .int _ v => v == 0
  | .str _ _ len => len == 0
  | .ptr 0 _ | .opaqueNil => true
  | .slice _ _ len _ => len == 0
  | .arr es => es.isEmpty
  | m@(.boxed 1 _) => match mapEntries heap m with | some (_, es) => es.isEmpty | none => false
  | _ => false

def jSep (parts : List (List Nat)) (sep : Nat) : List Nat := (parts.intersperse [sep]).flatten

/-- Go's HTML escaping of encoded JSON (`<`, `>`, `&`, U+2028, U+2029), as
`json.Marshal` applies it to a Marshaler's output (`json.RawMessage`). -/
def htmlEscape : List Nat → List Nat
  | 60 :: bs => asciiBytes "\\u003c" ++ htmlEscape bs
  | 62 :: bs => asciiBytes "\\u003e" ++ htmlEscape bs
  | 38 :: bs => asciiBytes "\\u0026" ++ htmlEscape bs
  | 0xE2 :: 0x80 :: 0xA8 :: bs => asciiBytes "\\u2028" ++ htmlEscape bs
  | 0xE2 :: 0x80 :: 0xA9 :: bs => asciiBytes "\\u2029" ++ htmlEscape bs
  | b :: bs => b :: htmlEscape bs
  | [] => []

/-- `omitzero`, and whether an SDK union variant is set (`param.IsOmitted`):
is `v` the zero value of entry `d`? A `param.Opt` is zero when omitted with
a zero value; the SDKs set `Value` only together with the status, so an
omitted Opt counts as zero also where the model does not know the value
(floats). `none`: unknown. -/
partial def jIsZero (t : JTable) (d : Nat) (v : Val) : Option Bool :=
  match t.descs[d]?, v with
  | some (.opt e), .structv (x :: .int _ st :: _) =>
    if st != 0 then some false else if jIsZero t e x == some false then some false else some true
  | some (.strct fields _ _), .structv vs => byFields fields vs
  | some (.union variants), .structv vs => byFields variants vs
  | _, _ => isZeroVal v
where
  /-- Described fields by their type, the others (metadata) as values. -/
  byFields (fields : List JField) (vs : List Val) : Option Bool :=
    let flags := vs.zipIdx.map fun (x, i) =>
      match fields.find? (·.idx == i) with
      | some f => jIsZero t f.ty x
      | none => isZeroVal x
    if flags.any (· == some false) then some false
    else if flags.all (· == some true) then some true else none

/-- `json.Marshal` of `v` as entry `d` of `t`: the bytes, `.error true` for
an error Go reports, `.error false` where the model is stuck. -/
partial def jEnc (t : JTable) (heap : Heap Val) (d : Nat) (v : Val) : Except Bool (List Nat) :=
  match t.descs[d]?, v with
  | none, _ => .error false
  | some _, .unit => .error false
  | some (.str dflt), s@(.str ..) =>
    match strBytes heap s with
    | some bs => .ok (goJsonQuote (if bs.isEmpty && !dflt.isEmpty then dflt else bs))
    | none => .error false
  | some .bool, .bool b => .ok (asciiBytes (if b then "true" else "false"))
  | some (.int _ signed), .int _ x => .ok (bvDec signed x)
  | some .bytes, .slice 0 .. | some .raw, .slice 0 .. | some (.slice _), .slice 0 .. => .ok (asciiBytes "null")
  | some .bytes, .slice ref off len _ =>
    match strBytes heap (.str ref off len) with
    | some bs => .ok ([34] ++ base64Enc bs ++ [34])
    | none => .error false
  | some .raw, .slice ref off len _ =>
    match strBytes heap (.str ref off len) with
    | some bs =>
      match Lean.Json.parse (utf8Lossy bs) with
      | .ok _ => .ok (htmlEscape (compactJson bs))
      | .error _ => .error true
    | none => .error false
  | some (.slice e), .slice ref off len _ =>
    match heap[ref]? with
    | some (.arr es) => elems e ((es.drop off.toNat).take len.toNat)
    | _ => .error false
  | some (.arr _ e), .arr es => elems e es
  | some (.ptr _), .ptr 0 _ => .ok (asciiBytes "null")
  | some (.ptr e), p@(.ptr ..) =>
    match heapLoad heap p with
    | .ok x => jEnc t heap e x
    | _ => .error false
  | some (.map _ _), .opaqueNil | some .iface, .opaqueNil => .ok (asciiBytes "null")
  | some (.map k vd), m@(.boxed 1 _) =>
    match mapEntries heap m with
    | some (_, es) => do
      let kd := t.descs[k]?.getD .opaque
      let mut parts : List (List Nat × List Nat) := []
      for (kv, x) in es do
        let key ← match kd, kv with
          | .str _, s@(.str ..) => (strBytes heap s).elim (.error false) .ok
          | .int _ sg, .int _ n => .ok (bvDec sg n)
          | _, _ => .error false
        parts := parts ++ [(key, ← jEnc t heap vd x)]
      let sorted := parts.mergeSort fun a b => bytesLe a.1 b.1
      .ok ([123] ++ jSep (sorted.map fun (key, x) => goJsonQuote key ++ [58] ++ x) 44 ++ [125])
    | none => .error false
  | some .iface, .boxed tag inner =>
    match t.boxed.lookup tag with
    | some d' => jEnc t heap d' inner
    | none => .error false
  | some (.strct fields _ _), .structv vs => do
    let ms ← members fields vs
    .ok ([123] ++ jSep (ms.map fun (key, x) => goJsonQuote key ++ [58] ++ x) 44 ++ [125])
  | some (.union variants), .structv vs =>
    let zero := variants.map fun f => (f, match vs[f.idx]? with
      | some x => jIsZero t f.ty x
      | none => some true)
    if zero.any (·.2 == none) then .error false else
    let present := (zero.filter (·.2 == some false)).map (·.1)
    match present with
    | [] => .ok (asciiBytes "null")
    | [f] => jEnc t heap f.ty (vs[f.idx]!)
    | _ => .error true
  | some (.opt e), .structv (x :: .int _ st :: _) =>
    if st.toNat == 2 then jEnc t heap e x else .ok (asciiBytes "null")
  | some _, _ => .error false
where
  elems (e : Nat) (es : List Val) : Except Bool (List Nat) := do
    let parts ← es.mapM (jEnc t heap e)
    .ok ([91] ++ jSep parts 44 ++ [93])
  /-- The encoded members of a struct: omitted ones left out, inline ones
  spliced in. -/
  members (fields : List JField) (vs : List Val) : Except Bool (List (List Nat × List Nat)) := do
    let mut out := []
    for f in fields do
      let x := vs[f.idx]?.getD .unit
      if f.inline then
        let inner := match x with
          | .ptr 0 _ => none
          | p@(.ptr ..) => match heapLoad heap p with | .ok y => some y | _ => none
          | y => some y
        let innerDesc := match t.descs[f.ty]? with
          | some (.ptr e) => t.descs[e]?
          | dd => dd
        match innerDesc, inner with
        | some (.strct fs _ _), some (.structv ws) => out := out ++ (← members fs ws)
        | _, none => pure ()
        | _, _ => .error false
      else if f.omitEmpty && jIsEmpty heap x then pure ()
      else if f.omitZero && jIsZero t f.ty x == some true then pure ()
      else if f.omitZero && jIsZero t f.ty x == none then .error false
      else out := out ++ [(f.key, ← jEnc t heap f.ty x)]
    return out

/-! Byte-level scanning of a valid JSON document: the source bytes of each
value. Go keeps them where it keeps raw text (`json.RawMessage`, the SDKs'
`raw` and `respjson.Field`), and checks integer literals on them; Lean's
parsed `Json` has sorted keys and normalised numbers and strings. -/

def jWs (b : Nat) : Bool := b == 32 || b == 9 || b == 10 || b == 13

def jSkipWs : List Nat → List Nat
  | b :: bs => if jWs b then jSkipWs bs else b :: bs
  | [] => []

/-- The value at the start of `bs` (no leading whitespace) and the rest. -/
def jSplitValue (bs : List Nat) : List Nat × List Nat := Id.run do
  let arr := bs.toArray
  let mut depth := 0
  let mut inStr := false
  let mut esc := false
  for i in [0:arr.size] do
    let b := arr[i]!
    if inStr then
      if esc then esc := false
      else if b == 92 then esc := true
      else if b == 34 then
        inStr := false
        if depth == 0 then return (bs.take (i + 1), bs.drop (i + 1))
    else if b == 34 then inStr := true
    else if b == 123 || b == 91 then depth := depth + 1
    else if b == 125 || b == 93 then
      if depth == 0 then return (bs.take i, bs.drop i)
      depth := depth - 1
      if depth == 0 then return (bs.take (i + 1), bs.drop (i + 1))
    else if depth == 0 && (b == 44 || b == 58 || jWs b) then return (bs.take i, bs.drop i)
  return (bs, [])

/-- The source of a whole document without surrounding whitespace. -/
def jTrim (bs : List Nat) : List Nat := (jSplitValue (jSkipWs bs)).1

/-- An object's members in source order: the key (decoded) and the value's
source. -/
partial def jObjMembers (src : List Nat) : List (String × List Nat) :=
  match src with
  | 123 :: rest => go (jSkipWs rest) []
  | _ => []
where
  go (bs : List Nat) (acc : List (String × List Nat)) : List (String × List Nat) :=
    match bs with
    | [] | 125 :: _ => acc.reverse
    | 44 :: rest => go (jSkipWs rest) acc
    | _ =>
      let (k, rest) := jSplitValue bs
      let key := match Lean.Json.parse (utf8Lossy k) with | .ok (.str s) => s | _ => ""
      match jSkipWs rest with
      | 58 :: rest =>
        let (v, rest) := jSplitValue (jSkipWs rest)
        go (jSkipWs rest) ((key, v) :: acc)
      | _ => acc.reverse

/-- An array's element sources in order. -/
partial def jArrElems (src : List Nat) : List (List Nat) :=
  match src with
  | 91 :: rest => go (jSkipWs rest) []
  | _ => []
where
  go (bs : List Nat) (acc : List (List Nat)) : List (List Nat) :=
    match bs with
    | [] | 93 :: _ => acc.reverse
    | 44 :: rest => go (jSkipWs rest) acc
    | _ => let (v, rest) := jSplitValue bs; go (jSkipWs rest) (v :: acc)

/-- Go's integer literal syntax (strconv.ParseInt on the literal): an
optional minus and digits only (`1e2`, `1.0` do not decode into an int). -/
def jIntLiteral (src : List Nat) : Bool :=
  let ds := match src with | 45 :: ds => ds | ds => ds
  !ds.isEmpty && ds.all fun b => 48 ≤ b && b ≤ 57

/-- A respjson.Field: its status (1 null, 2 invalid, 3 valid) and raw text. -/
def respField (heap : Heap Val) (status : Nat) (src : List Nat) : Val × Heap Val :=
  let (r, heap) := strAlloc heap src
  (.structv [.int 8 (BitVec.ofNat 8 status), r], heap)

/-- Decode `j` (whose source bytes are `src`) into the current value `cur`
of entry `d`: the new value, the heap, and whether some member had the
wrong JSON type (Go keeps going and reports the first such error).
`.error ()`: the model is stuck. -/
partial def jDec (t : JTable) (heap : Heap Val) (d : Nat) (j : Lean.Json) (src : List Nat) (cur : Val) :
    Except Unit (Val × Heap Val × Bool) :=
  let keep := Except.ok (cur, heap, false)
  let typeErr := Except.ok (cur, heap, true)
  match t.descs[d]?, j with
  | none, _ => .error ()
  | some (.str _), .str s => let (v, heap) := strAlloc heap (asciiBytes s); .ok (v, heap, false)
  | some (.str _), .null | some .bool, .null | some (.int ..), .null | some .float, .null => keep
  | some (.str _), _ | some .bool, _ | some (.int ..), _ | some .float, _ =>
    match t.descs[d]?, j with
    | some .bool, .bool b => .ok (.bool b, heap, false)
    | some (.int w signed), .num n =>
      if n.exponent != 0 || !jIntLiteral src then typeErr else
      let m := n.mantissa
      let ok := if signed then -(2 ^ (w - 1) : Int) ≤ m && m < 2 ^ (w - 1) else 0 ≤ m && m < 2 ^ w
      if ok then .ok (.int w (BitVec.ofInt w m), heap, false) else typeErr
    | some .float, .num _ => .ok (.unit, heap, false)
    | _, _ => typeErr
  | some .bytes, .null => .ok (.slice 0 0#64 0#64 0#64, heap, false)
  | some .bytes, .str s =>
    match base64Dec (asciiBytes s) with
    | some bs => .ok (byteSlice heap bs)
    | none => typeErr
  | some .bytes, _ => typeErr
  | some .raw, _ => .ok (byteSlice heap src)
  | some (.slice _), .null => .ok (.slice 0 0#64 0#64 0#64, heap, false)
  | some (.slice e), .arr js => do
    let srcs := jArrElems src
    if srcs.length != js.size then .error () else
    let (vs, heap, err) ← decList e (js.toList.zip srcs) (List.replicate js.size (t.zeros[e]?.getD .unit)) heap
    let (p, heap) := heapAlloc heap (.arr vs)
    match p with
    | .ptr ref _ => .ok (.slice ref 0#64 (BitVec.ofNat 64 vs.length) (BitVec.ofNat 64 vs.length), heap, err)
    | _ => .error ()
  | some (.slice _), _ => typeErr
  | some (.arr n e), .arr js =>
    match cur with
    | .arr old => do
      let srcs := jArrElems src
      if srcs.length != js.size then .error () else
      let js := (js.toList.zip srcs).take n
      let (vs, heap, err) ← decList e js (old.take js.length) heap
      .ok (.arr (vs ++ (old.drop js.length).map fun _ => t.zeros[e]?.getD .unit), heap, err)
    | _ => .error ()
  | some (.arr ..), .null => keep
  | some (.arr ..), _ => typeErr
  | some (.ptr _), .null => .ok (.ptr 0 [], heap, false)
  | some (.ptr e), j' =>
    match cur with
    | .ptr 0 _ => do
      let (v, heap, err) ← jDec t heap e j' src (t.zeros[e]?.getD .unit)
      let (p, heap) := heapAlloc heap v
      .ok (p, heap, err)
    | p@(.ptr ..) =>
      match heapLoad heap p with
      | .ok old => do
        let (v, heap, err) ← jDec t heap e j' src old
        match heapStore heap p v with
        | .ok heap => .ok (p, heap, err)
        | _ => .error ()
      | _ => .error ()
    | _ => .error ()
  | some (.map _ _), .null => .ok (.opaqueNil, heap, false)
  | some (.map k vd), .obj kvs => do
    let members := jObjMembers src
    -- Duplicate keys: Go decodes each occurrence in turn; Lean's Json
    -- keeps the last one only.
    if members.length != kvs.size then .error () else
    let (m, heap) := match cur with
      | .opaqueNil => let (p, heap) := heapAlloc heap (mapObj []); (Val.boxed 1 p, heap)
      | m => (m, heap)
    let (q, old) ← (mapEntries heap m).elim (.error ()) .ok
    let mut es := old
    let mut heap := heap
    let mut err := false
    for (key, vsrc) in members do
      let jv ← (kvs.get? key).elim (.error ()) .ok
      let (kv, heap') ← match t.descs[k]? with
        | some (.str _) => .ok (strAlloc heap (asciiBytes key))
        | some (.int w sg) =>
          match atoiBytes (asciiBytes key) with
          | some n => if sg || n ≥ 0 then .ok (.int w (BitVec.ofInt w n), heap) else .error ()
          | none => .error ()
        | _ => .error ()
      let (v, heap'', e) ← jDec t heap' vd jv vsrc (t.zeros[vd]?.getD .unit)
      heap := heap''
      err := err || e
      es := if es.any (fun (k', _) => keyEq heap kv k') then
          es.map fun (k', v') => if keyEq heap kv k' then (k', v) else (k', v')
        else es ++ [(kv, v)]
    match heapStore heap q (mapObj es) with
    | .ok h => .ok (m, h, err)
    | _ => .error ()
  | some (.map ..), _ => typeErr
  | some .iface, j' => let (v, heap) := jsonToVal t.basic heap j'; .ok (v, heap, false)
  | some (.strct _ _ false), .null => keep
  | some (.strct fields md api), .obj kvs =>
    match cur with
    | .structv vs => do
      let members := jObjMembers src
      if members.length != kvs.size then .error () else
      let (vs, heap, err, metas) ←
        if api then decApi fields members kvs vs heap else do
          let (vs, heap, err) ← decStruct fields members kvs vs heap
          pure (vs, heap, err, members.map fun (k, v) =>
            (asciiBytes k, (if kvs.get? k matches some .null then 1 else 3, v)))
      -- An SDK response type: its JSON metadata.
      match md with
      | none => .ok (.structv vs, heap, err)
      | some mt =>
        match vs[mt.field]? with
        | some (.structv ms) =>
          let (raw, heap) := strAlloc heap src
          let ms := ms.set mt.raw raw
          let (ms, heap) := mt.members.foldl (fun (ms, heap) (key, i) =>
            match metas.find? (fun (k, _) => k == key) with
            | some (_, (st, vsrc)) => let (f, heap) := respField heap st vsrc; (ms.set i f, heap)
            | none => (ms, heap)) (ms, heap)
          let known := knownKeys fields
          let extras := members.filter fun (k, _) => !known.contains (asciiBytes k)
          let (ms, heap) := match mt.extra with
            | some ei =>
              if extras.isEmpty then (ms, heap) else
              let (es, heap) := extras.foldl (fun (acc, heap) (k, vsrc) =>
                let (kv, heap) := strAlloc heap (asciiBytes k)
                -- apijson has no decoder for an extra key: invalid unless null.
                let st := if (kvs.get? k) matches some .null then 1 else 2
                let (f, heap) := respField heap st vsrc
                (acc ++ [(kv, f)], heap)) ([], heap)
              let (p, heap) := heapAlloc heap (mapObj es)
              (ms.set ei (.boxed 1 p), heap)
            | none => (ms, heap)
          .ok (.structv (vs.set mt.field (.structv ms)), heap, err)
        | _ => .ok (.structv vs, heap, err)
    | _ => .error ()
  | some (.strct _ _ true), _ => .error ()
  | some (.strct ..), _ => typeErr
  | some (.opt _), .null =>
    match cur with
    | .structv (x :: _ :: rest) => .ok (.structv (x :: .int 8 1#8 :: rest), heap, false)
    | _ => .error ()
  | some (.opt e), j' =>
    match cur with
    | .structv (x :: _ :: rest) => do
      let (v, heap, err) ← jDec t heap e j' src x
      -- Opt.UnmarshalJSON leaves the option as it was when the value
      -- does not decode.
      if err then .ok (cur, heap, true) else
      .ok (.structv (v :: .int 8 2#8 :: rest), heap, false)
    | _ => .error ()
  | some _, _ => .error ()
where
  byteSlice (heap : Heap Val) (bs : List Nat) : Val × Heap Val × Bool :=
    let (p, heap) := heapAlloc heap (.arr (bs.map fun b => .int 8 (BitVec.ofNat 8 b)))
    match p with
    | .ptr ref _ => (.slice ref 0#64 (BitVec.ofNat 64 bs.length) (BitVec.ofNat 64 bs.length), heap, false)
    | _ => (.unit, heap, false)
  decList (e : Nat) : List (Lean.Json × List Nat) → List Val → Heap Val → Except Unit (List Val × Heap Val × Bool)
    | (j, s) :: js, c :: cs, heap => do
      let (v, heap, e1) ← jDec t heap e j s c
      let (vs, heap, e2) ← decList e js cs heap
      .ok (v :: vs, heap, e1 || e2)
    | _, _, heap => .ok ([], heap, false)
  /-- The keys the struct's fields (inline ones included) decode. -/
  knownKeys (fields : List JField) : List (List Nat) :=
    fields.flatMap fun f =>
      if f.inline then
        match t.descs[f.ty]? with
        | some (.strct fs _ _) => knownKeys fs
        | some (.ptr e) => match t.descs[e]? with | some (.strct fs _ _) => knownKeys fs | _ => []
        | _ => []
      else [f.key]
  /-- Members into fields (encoding/json): the exact key, else a
  case-insensitive match; inline fields take the whole object. -/
  decStruct (fields : List JField) (members : List (String × List Nat)) (kvs : Std.TreeMap.Raw String Lean.Json)
      (vs : List Val) (heap : Heap Val) : Except Unit (List Val × Heap Val × Bool) := do
    let mut vs := vs
    let mut heap := heap
    let mut err := false
    for f in fields do
      if f.inline then
        let (v, heap', e) ← jDec t heap f.ty (.obj kvs) (wholeObject members) (vs[f.idx]?.getD .unit)
        vs := vs.set f.idx v
        heap := heap'
        err := err || e
      else
        let hit := (members.find? fun (k, _) => asciiBytes k == f.key) <|>
          (members.find? fun (k, _) => (asciiBytes k).map lowerByte == f.key.map lowerByte)
        match hit with
        | some (k, vsrc) =>
          let jv ← (kvs.get? k).elim (.error ()) .ok
          let (v, heap', e) ← jDec t heap f.ty jv vsrc (vs[f.idx]?.getD .unit)
          vs := vs.set f.idx v
          heap := heap'
          err := err || e
        | none => pure ()
    return (vs, heap, err)
  /-- Members into fields as the SDKs' apijson decoder does: exact keys
  only; null leaves a field unchanged (an Opt becomes null); pointers and
  maps are decoded afresh; a member that does not decode is where the
  model gets stuck (apijson would coerce it or mark it invalid). Also the
  status and source of each typed member, for the metadata. -/
  decApi (fields : List JField) (members : List (String × List Nat)) (kvs : Std.TreeMap.Raw String Lean.Json)
      (vs : List Val) (heap : Heap Val) :
      Except Unit (List Val × Heap Val × Bool × List (List Nat × (Nat × List Nat))) := do
    let mut vs := vs
    let mut heap := heap
    let mut metas := []
    for f in fields do
      if f.inline then
        match t.descs[f.ty]? with
        | some (.strct ..) =>
          let (v, heap', e) ← jDec t heap f.ty (.obj kvs) (wholeObject members) (vs[f.idx]?.getD .unit)
          if e then .error ()
          vs := vs.set f.idx v
          heap := heap'
        | _ => .error () -- a variant decoded from the whole value: not modelled
      else
        match members.find? fun (k, _) => asciiBytes k == f.key with
        | some (k, vsrc) =>
          let jv ← (kvs.get? k).elim (.error ()) .ok
          if jv matches .null then
            if let some (.opt _) := t.descs[f.ty]? then
              let (v, heap', _) ← jDec t heap f.ty jv vsrc (vs[f.idx]?.getD .unit)
              vs := vs.set f.idx v
              heap := heap'
            metas := metas ++ [(f.key, (1, vsrc))]
          else
            let start := match t.descs[f.ty]? with
              | some (.ptr _) => .ptr 0 []
              | some (.map ..) => .opaqueNil
              | _ => vs[f.idx]?.getD .unit
            let (v, heap', e) ← jDec t heap f.ty jv vsrc start
            if e then .error ()
            vs := vs.set f.idx v
            heap := heap'
            metas := metas ++ [(f.key, (3, vsrc))]
        | none => pure ()
    return (vs, heap, false, metas)
  /-- An object's source rebuilt from its members (for inline fields, which
  read the whole object). -/
  wholeObject (members : List (String × List Nat)) : List Nat :=
    [123] ++ jSep (members.map fun (k, v) => goJsonQuote (asciiBytes k) ++ [58] ++ v) 44 ++ [125]

/-- All instructions: heap-touching ones here, the rest via `denotePure`. -/
def denote (or : Oracle) (heap : Heap Val) : Op → List Val → Res (Val × Heap Val)
  | .alloc zero, [] => .ok (heapAlloc heap zero)
  | .makeMap, [] =>
    let (p, heap) := heapAlloc heap (.arr [])
    .ok (.boxed 1 p, heap)
  | .jsonMarshal t root, [v] =>
    match jEnc t heap root v with
    | .ok bs =>
      let (p, heap) := heapAlloc heap (.arr (bs.map fun b => .int 8 (BitVec.ofNat 8 b)))
      match p with
      | .ptr ref _ => .ok (.tuple [.slice ref 0#64 (BitVec.ofNat 64 bs.length) (BitVec.ofNat 64 bs.length), .err false], heap)
      | _ => .panic .modelStuck
    | .error true => .ok (.tuple [.slice 0 0#64 0#64 0#64, .err true], heap)
    | .error false => .panic .modelStuck
  | .jsonDecode t root, [data, p] =>
    match data with
    | .slice ref off len _ =>
      match strBytes heap (.str ref off len) with
      | some bs =>
        match Lean.Json.parse (utf8Lossy bs) with
        | .error _ => .ok (.err true, heap)
        | .ok j =>
          match heapLoad heap p with
          | .ok cur =>
            match jDec t heap root j (jTrim bs) cur with
            | .ok (v, heap, err) =>
              match heapStore heap p v with
              | .ok heap => .ok (.err err, heap)
              | .panic k => .panic k
              | .outOfFuel => .outOfFuel
            | .error () => .panic .modelStuck
          | .panic k => .panic k
          | .outOfFuel => .outOfFuel
      | none => .panic .modelStuck
    | _ => .panic .modelStuck
  | .jsonUnmarshal kind tags, [data, p] =>
    match data with
    | .slice ref off len _ =>
      match strBytes heap (.str ref off len) with
      | some bs => jsonUnmarshalOp heap kind tags bs p
      | none => .panic .modelStuck
    | _ => .panic .modelStuck
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
    | some x, some y => if x.length + y.length > strLimit then .panic .modelStuck else .ok (strAlloc heap (x ++ y))
    | _, _ => .panic .modelStuck
  | .eqDeep, [a, b] => match eqDeep heap a b with
    | some r => .ok (.bool r, heap) | none => .panic .modelStuck
  | .neDeep, [a, b] => match eqDeep heap a b with
    | some r => .ok (.bool !r, heap) | none => .panic .modelStuck
  | .sprintfOk, [f, args] =>
    match strBytes heap f, sliceElems heap args with
    | some fb, some xs => .ok (.bool ((sprintfBytes heap fb xs).isSome), heap)
    | _, _ => .ok (.bool false, heap)
  | .sprintf, [f, args] =>
    match strBytes heap f, sliceElems heap args with
    | some fb, some xs =>
      match sprintfBytes heap fb xs with
      | some r => if r.length > strLimit then .panic .modelStuck else .ok (strAlloc heap r)
      | none => .panic .modelStuck
    | _, _ => .panic .modelStuck
  | .hexEncode, [.slice ref off len _] =>
    match strBytes heap (.str ref off len) with
    | some x =>
      let digit (d : Nat) := if d < 10 then 48 + d else 87 + d
      .ok (strAlloc heap (x.flatMap fun b => [digit (b / 16), digit (b % 16)]))
    | none => .panic .modelStuck
  | .freshArr n, [] =>
    let k := heap.size
    let (_, heap) := heapAlloc heap .unit
    .ok (.arr ((List.range n).map fun i => .int 8 (BitVec.ofNat 8 (k / 256 ^ (n - 1 - i)))), heap)
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
    | some x, some old, some new =>
      let r := replaceBytes x old new
      if r.length > strLimit then .panic .modelStuck else .ok (strAlloc heap r)
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
      if old.length + bs.length > strLimit then .panic .modelStuck else
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
      if old.length ≥ strLimit then .panic .modelStuck else
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
      if old.length + enc.length > strLimit then .panic .modelStuck else
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
    | some xs, some y =>
      let r := (xs.intersperse y).flatten
      if r.length > strLimit then .panic .modelStuck else .ok (strAlloc heap r)
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
  callee
    | .boxed 0 (.tuple (.int _ f :: bound)) => .known f.toNat bound
    | .opaqueNil => .nil
    | _ => .unknown

abbrev Prog := SSA.Prog Op
abbrev Func := SSA.Func Op
abbrev Block := SSA.Block Op
abbrev Instr := SSA.Instr Op

end Go2Lean.GoSSA
