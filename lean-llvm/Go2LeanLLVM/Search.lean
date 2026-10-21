import Lean.Data.Json
import Go2LeanLLVM.Dialect

/-!
# Search over LLVM models

The engines of the Go line, for functions of an LLVM model: seeded random
trials and, when the domain is small enough, exhaustive enumeration. The
property is that the function runs without undefined behaviour. A pointer
parameter points to a fresh object (a buffer of elements); an integer
parameter the frontend paired with it is its element count, so a caller
that passes the buffer's real length is all the search tries.
-/

namespace Go2Lean.LLVM.Search

open Go2Lean.SSA Go2Lean.LLVM Lean

/-- How to generate one parameter. Mirrors the frontend's ABI. -/
inductive Gen where
  | int (name : String) (w : Nat)
  /-- A pointer to a fresh object of `n ≤ maxLen` elements of `elemBytes`
  bytes each. -/
  | buf (name : String) (elemBytes : Nat) (maxLen : Nat)
  /-- The element count of buffer parameter `of` (an index into the
  generators), as a `w`-bit integer. -/
  | len (name : String) (w : Nat) (of : Nat)
  deriving Repr, Inhabited

def Gen.name : Gen → String
  | .int n _ | .buf n _ _ | .len n _ _ => n

/-- A generated argument before it is placed in memory. -/
inductive Arg where
  | int (w : Nat) (v : Int)
  | buf (elemBytes : Nat) (elems : List Int)
  deriving Repr, Inhabited

/-- Interesting values of a `w`-bit integer. -/
def specials (w : Nat) : List Int :=
  let lim := (2 : Int) ^ (w - 1)
  [0, 1, -1, 2, lim - 1, -lim, lim / 2, 7, 255, 256, 65535, 1024]

def randInt (g : StdGen) (w : Nat) : Int × StdGen :=
  let (k, g) := randNat g 0 9
  if k < 4 then
    let sp := specials w
    let (i, g) := randNat g 0 (sp.length - 1)
    (sp[i]!, g)
  else if k < 8 then
    let (v, g) := randNat g 0 16
    let (s, g) := randNat g 0 1
    (if s == 0 then v else -(v : Int), g)
  else
    let (v, g) := randNat g 0 (2 ^ w - 1)
    ((BitVec.ofNat w v).toInt, g)

/-- Random arguments: buffers first (their lengths feed `len`). -/
def genArgs (gens : List Gen) (g : StdGen) : List Arg × StdGen := Id.run do
  let mut g := g
  let mut out : Array Arg := #[]
  for gen in gens do
    match gen with
    | .int _ w =>
      let (v, g') := randInt g w
      g := g'; out := out.push (.int w v)
    | .buf _ eb maxLen =>
      let (n, g') := randNat g 0 maxLen
      g := g'
      let mut es : Array Int := #[]
      for _ in [0:n] do
        let (v, g') := randInt g (eb * 8)
        g := g'; es := es.push v
      out := out.push (.buf eb es.toList)
    | .len _ w _ => out := out.push (.int w 0)
  -- Each length is its buffer's element count.
  let fixed := (gens.zip out.toList).map fun (gen, a) =>
    match gen with
    | .len _ w of => match out[of]? with
      | some (.buf _ es) => Arg.int w es.length
      | _ => a
    | _ => a
  return (fixed, g)

/-- Place the arguments: buffers become fresh objects in memory. -/
def materialise (args : List Arg) : Option (List Val × Heap Val) := do
  let mut m := Veir.MemoryState.empty
  let mut vals : Array Val := #[]
  for a in args do
    match a with
    | .int w v => vals := vals.push (.int w (.val (BitVec.ofInt w v)))
    | .buf eb es =>
      let (m', p) ← (match m.alloc (eb * es.length).toUInt64 with | .ok r => some r | _ => none)
      m := m'
      let mut i := 0
      for e in es do
        let q : Veir.Data.Pointer := ⟨p.object, p.address + (i * eb).toUInt64⟩
        m ← (match m.llvmStore q (.int (eb * 8) (.val (BitVec.ofInt (eb * 8) e))) with
          | .ok m => some m | _ => none)
        i := i + 1
      vals := vals.push (.ptr (.val p))
  return (vals.toList, #[.unit, .mem m])

/-- Havoc values for unmodelled calls: `w`-bit integers from the seed. -/
def havocOf (seed : Nat) (widths : Array Nat) : Havoc Val := fun site iter =>
  let w := widths[site]?.getD 64
  let g := mkStdGen (seed * 1000003 + site * 7919 + iter)
  .int w (.val (BitVec.ofInt w (randInt g w).1))

inductive Outcome where
  | holds
  | ub (k : PanicKind)
  | stuck
  | outOfFuel

def runOnce (p : Prog) (fid : FuncId) (args : List Arg) (seed fuel : Nat) : Outcome × HState Val :=
  match materialise args with
  | none => (.stuck, {})
  | some (vals, heap) =>
    let s : HState Val := { heap := heap }
    match runFunc p () (havocOf seed p.havocWidths) fid vals s fuel with
    | (.ok _, s) => (.holds, s)
    | (.panic k, s) => (if k.isStuck then .stuck else .ub k, s)
    | (.outOfFuel, s) => (.outOfFuel, s)

def argJson (gen : Gen) (a : Arg) : List (String × Json) × List (String × Json) :=
  match gen, a with
  | _, .int _ v => ([(gen.name, Json.str (toString v))], [])
  | _, .buf _ es => ([], [(gen.name, Json.arr (es.map (Json.str ∘ toString)).toArray)])

def report (gens : List Gen) (args : List Arg) (k : PanicKind) (s : HState Val)
    (trial seed : Nat) (engine : String) : Json :=
  let parts := (gens.zip args).map fun (g, a) => argJson g a
  Json.mkObj [("found", true), ("engine", engine), ("trial", trial), ("seed", seed),
    ("inputs", Json.mkObj (parts.flatMap (·.1))), ("memory", Json.mkObj (parts.flatMap (·.2))),
    ("model_output", Json.mkObj [("res", "panic"), ("panic", k.tag)]),
    ("panic_at", (s.panicAt.map Json.str).getD Json.null),
    ("havoc", Json.arr (s.log.map fun (site, iter, v) =>
      Json.mkObj [("idx", site), ("iter", iter), ("value", (repr v).pretty)]))]

/-- Small values for exhaustive enumeration. -/
def smallInts (w : Nat) : List Int :=
  let lim := (2 : Int) ^ (w - 1)
  if w ≤ 2 then (List.range (2 ^ w)).map (fun n => (BitVec.ofNat w n).toInt)
  else [0, 1, -1, 2, lim - 1, -lim]

def tuples (xs : List α) : Nat → List (List α)
  | 0 => [[]]
  | n + 1 => xs.flatMap fun x => (tuples xs n).map (x :: ·)

def genChoices (exLen : Nat) : Gen → List Arg
  | .int _ w => (smallInts w).map (.int w)
  | .buf _ eb maxLen =>
    (List.range (min exLen maxLen + 1)).flatMap fun n =>
      (tuples [0, 1, -1] n).map (.buf eb)
  | .len _ w _ => [.int w 0]

def domainSize (exLen : Nat) (gens : List Gen) : Nat :=
  gens.foldl (fun acc g => acc * (genChoices exLen g).length) 1

def enumArgs (exLen : Nat) (gens : List Gen) : List (List Arg) :=
  let raw := gens.foldr (fun g acc => (genChoices exLen g).flatMap fun a => acc.map (a :: ·)) [[]]
  raw.map fun args => (gens.zip args).map fun (gen, a) =>
    match gen with
    | .len _ w of => match args[of]? with
      | some (.buf _ es) => Arg.int w es.length
      | _ => a
    | _ => a

structure Spec where
  func : FuncId
  gens : List Gen
  trials : Nat := 2000
  seed : Nat := 42
  fuel : Nat := 5000
  engine : String := "all"
  exhaustLen : Nat := 3
  exhaustLimit : Nat := 300000

def search (p : Prog) (sp : Spec) : Json := Id.run do
  let mut stuck := 0
  let mut fuelOut := 0
  if sp.engine != "exhaustive" then
    let mut g := mkStdGen sp.seed
    for t in [0:sp.trials] do
      let (args, g') := genArgs sp.gens g
      g := g'
      match runOnce p sp.func args (sp.seed + t) sp.fuel with
      | (.ub k, s) => return report sp.gens args k s t sp.seed "random"
      | (.stuck, _) => stuck := stuck + 1
      | (.outOfFuel, _) => fuelOut := fuelOut + 1
      | _ => pure ()
  let dom := domainSize sp.exhaustLen sp.gens
  let mut exhausted := false
  if sp.engine != "random" && dom ≤ sp.exhaustLimit then
    let mut i := 0
    let mut clean := true
    for args in enumArgs sp.exhaustLen sp.gens do
      match runOnce p sp.func args 0 sp.fuel with
      | (.ub k, s) => return report sp.gens args k s i 0 "exhaustive"
      | (.holds, _) => pure ()
      | _ => clean := false
      i := i + 1
    exhausted := clean
  return Json.mkObj [("found", false), ("trials", sp.trials), ("stuck", stuck),
    ("out_of_fuel", fuelOut), ("domain", dom), ("exhausted", exhausted)]

/-- Decode a generator; the shape mirrors the frontend's ABI. -/
def genOfJson (j : Json) : Except String Gen := do
  let name ← j.getObjValAs? String "name"
  match ← j.getObjValAs? String "k" with
  | "int" => return .int name (← j.getObjValAs? Nat "w")
  | "buf" => return .buf name (← j.getObjValAs? Nat "eb") (← j.getObjValAs? Nat "maxLen")
  | "len" => return .len name (← j.getObjValAs? Nat "w") (← j.getObjValAs? Nat "of")
  | k => throw s!"unknown generator {k}"

def specOfJson (j : Json) : Except String Spec := do
  let gens ← (← j.getObjValAs? (Array Json) "gens").toList.mapM genOfJson
  let opt (k : String) (d : Nat) : Nat := (j.getObjValAs? Nat k).toOption.getD d
  return { func := ← j.getObjValAs? Nat "func", gens,
           trials := opt "trials" 2000, seed := opt "seed" 42, fuel := opt "fuel" 5000,
           engine := (j.getObjValAs? String "engine").toOption.getD "all",
           exhaustLen := opt "exhaustLen" 3, exhaustLimit := opt "exhaustLimit" 300000 }

/-- The driver: `go2lean-llvm-search <spec.json>` with the spec naming a
model of the registry. -/
def driverMain (registry : List (String × Prog)) (args : List String) : IO UInt32 := do
  let [path] := args | IO.eprintln "usage: go2lean-llvm-search <spec.json>"; return 2
  let j ← IO.ofExcept (Json.parse (← IO.FS.readFile path))
  let model ← IO.ofExcept (j.getObjValAs? String "model")
  let some p := registry.lookup model | IO.eprintln s!"no model {model}"; return 2
  let sp ← IO.ofExcept (specOfJson j)
  IO.println (search p sp).compress
  return 0

end Go2Lean.LLVM.Search
