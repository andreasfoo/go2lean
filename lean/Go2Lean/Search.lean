import Lean.Data.Json
import Go2Lean.Dialect.GoSSA

/-!
# Counterexample search driver

M2's cheapest engine: seeded random testing of a property over a `GoSSA`
program, printing a JSON counterexample to stdout. The Go side generates a
small file that imports a model and calls `Search.run`.

The havoc oracle is derived from the seed, so a counterexample is
reproducible; the values actually consumed are reported from the run log.
-/
namespace Go2Lean.Search

open Go2Lean.SSA Go2Lean.GoSSA Lean

/-- How to generate one Go parameter. Mirrors `contract.ABIParam`. -/
inductive Gen where
  | int (name : String) (w : Nat) (signed : Bool)
  | bool (name : String)
  /-- Slice of `w`-bit integers; `maxLen` bounds the generated length. -/
  | slice (name : String) (w : Nat) (signed : Bool) (maxLen : Nat)

/-- The property shape to check. -/
inductive Property where
  | noPanic (f : FuncId)
  /-- `spec` takes the arguments of `f` followed by its result and returns a bool. -/
  | postcondition (f spec : FuncId)

inductive Outcome where
  | holds
  | violated
  | panicked (k : PanicKind)
  | specPanicked (k : PanicKind)
  | outOfFuel
  deriving Repr

-- Generation -----------------------------------------------------------------

/-- Boundary values a random search should hit far more often than chance would. -/
def specials (w : Nat) : Array (BitVec w) :=
  #[0#w, 1#w, -1#w, BitVec.intMin w, BitVec.intMax w, BitVec.intMax w - 1#w, BitVec.intMin w + 1#w]

/-- A value profile biases every value of one trial the same way, so that
inputs that must be correlated to trigger a bug (several large elements, all
small indices) are reached far sooner than by independent sampling. -/
inductive Profile where
  | boundary -- only `specials`
  | small    -- magnitudes up to 8
  | mixed    -- 40% specials, otherwise uniform
  deriving Repr

def Profile.ofNat : Nat → Profile
  | 0 => .boundary
  | 1 => .small
  | _ => .mixed

/-- Literals mined from the program, with their off-by-one neighbours, as
`w`-bit values. These join the boundary values so that comparisons against
magic constants are reachable by random search. -/
def mined (w : Nat) (extra : List Nat) : Array (BitVec w) :=
  (extra.flatMap fun c => [BitVec.ofNat w c, BitVec.ofNat w c - 1#w, BitVec.ofNat w c + 1#w]).toArray

def genBits (pf : Profile) (extra : List Nat) (g : StdGen) (w : Nat) : BitVec w × StdGen :=
  let (pick, g) := randNat g 0 9
  let pool := specials w ++ mined w extra
  let special (g : StdGen) :=
    let (i, g) := randNat g 0 pool.size.pred
    (pool[i]!, g)
  match pf with
  | .boundary => special g
  | .small =>
    let (n, g) := randNat g 0 16
    (BitVec.ofInt w (Int.ofNat n - 8), g)
  | .mixed =>
    if pick < 4 then special g
    else
      let (n, g) := randNat g 0 (2 ^ w - 1)
      (BitVec.ofNat w n, g)

def genVal (pf : Profile) (extra : List Nat) (g : StdGen) (fresh : Nat) : Gen → Val × StdGen × Nat
  | .int _ w _ =>
    let (v, g) := genBits pf extra g w
    (.int w v, g, fresh)
  | .bool _ =>
    let (b, g) := randBool g
    (.bool b, g, fresh)
  | .slice _ _ _ maxLen =>
    let (len, g) := randNat g 0 maxLen
    let (extra, g) := randNat g 0 2
    (.slice fresh 0#64 (BitVec.ofNat 64 len) (BitVec.ofNat 64 (len + extra)), g, fresh + 1)

def genArgs (pf : Profile) (extra : List Nat) (g : StdGen) : List Gen → List Val × StdGen
  | gens => go gens g 0 []
where
  go : List Gen → StdGen → Nat → List Val → List Val × StdGen
    | [], g, _, acc => (acc.reverse, g)
    | gen :: rest, g, fresh, acc =>
      let (v, g, fresh) := genVal pf extra g fresh gen
      go rest g fresh (v :: acc)

/-- Memory contents derived from a per-trial seed and profile. -/
def oracleOf (pf : Profile) (extra : List Nat) (seed : Nat) : Oracle :=
  { mem := fun ref idx =>
      let h := mixHash (mixHash (UInt64.ofNat (seed + 7)) (UInt64.ofNat ref)) (UInt64.ofNat idx.toNat)
      (genBits pf extra (mkStdGen h.toNat) 64).1 }

/-- A havoc oracle derived from the seed. -/
def havocOf (seed : Nat) (widths : Array Nat) : Havoc Val := fun site iter =>
  let w := widths[site]?.getD 64
  let h := mixHash (mixHash (UInt64.ofNat seed) (UInt64.ofNat site)) (UInt64.ofNat (iter + 1))
  let g := mkStdGen h.toNat
  let (v, _) := genBits .mixed [] g w
  .int w v

-- Checking -------------------------------------------------------------------

def check (p : GoSSA.Prog) (or : Oracle) (h : Havoc Val) (prop : Property) (args : List Val)
    (fuel : Nat) : Outcome × HState Val × Option Val :=
  match prop with
  | .noPanic f =>
    match run p or h f args fuel with
    | (.ok v, s) => (.holds, s, some v)
    | (.panic k, s) => (.panicked k, s, none)
    | (.outOfFuel, s) => (.outOfFuel, s, none)
  | .postcondition f spec =>
    match run p or h f args fuel with
    | (.ok v, s) =>
      match runFunc p or h spec (args ++ [v]) s fuel with
      | (.ok (.bool true), s) => (.holds, s, some v)
      | (.ok (.bool false), s) => (.violated, s, some v)
      | (.ok _, s) => (.specPanicked .modelStuck, s, some v)
      | (.panic k, s) => (.specPanicked k, s, some v)
      | (.outOfFuel, s) => (.outOfFuel, s, some v)
    | (.panic k, s) => (.panicked k, s, none)
    | (.outOfFuel, s) => (.outOfFuel, s, none)

/-- Does the outcome refute the property? -/
def Outcome.refutes : Property → Outcome → Bool
  | .noPanic _, .panicked _ => true
  | .postcondition .., .violated => true
  | _, _ => false

-- Reporting ------------------------------------------------------------------

def renderInt (signed : Bool) (v : BitVec w) : String :=
  if signed then toString v.toInt else toString v.toNat

/-- Flatten one argument into the model parameter names M3 expects. -/
def flatten : Gen → Val → List (String × Json)
  | .int name _ signed, .int _ v => [(name, renderInt signed v)]
  | .bool name, .bool b => [(name, toString b)]
  | .slice name _ _ _, .slice ref off len cap =>
    [(name ++ "_ref", toString ref), (name ++ "_off", renderInt false off),
     (name ++ "_len", renderInt false len), (name ++ "_cap", renderInt false cap)]
  | g, _ => [(genName g, "?")]
where
  genName : Gen → String
    | .int n .. => n | .bool n => n | .slice n .. => n

/-- The slice contents the oracle holds for each slice argument, keyed by
parameter name, so M3 can materialise them. -/
def memoryOf (or : Oracle) (gens : List Gen) (args : List Val) : List (String × Json) :=
  (gens.zip args).filterMap fun
    | (.slice name w signed _, .slice ref off len _) =>
      let elems := (List.range len.toNat).map fun j =>
        Json.str (renderInt signed ((or.mem ref (off + BitVec.ofNat 64 j)).zeroExtend w))
      some (name, Json.arr elems.toArray)
    | _ => none

def renderVal : Val → Json
  | .int _ v => Json.str (renderInt true v)
  | .bool b => Json.str (toString b)
  | .slice ref off len cap =>
    Json.str s!"slice(ref={ref}, off={off.toNat}, len={len.toNat}, cap={cap.toNat})"
  | .unit => Json.str "unit"

def modelOutput : Outcome → Option Val → Json
  | .panicked k, _ => Json.mkObj [("res", "panic"), ("panic", k.tag)]
  | .specPanicked k, v =>
    Json.mkObj [("res", "spec_panic"), ("panic", k.tag), ("value", (v.map renderVal).getD Json.null)]
  | .outOfFuel, _ => Json.mkObj [("res", "out_of_fuel")]
  | _, v => Json.mkObj [("res", "ok"), ("value", (v.map renderVal).getD Json.null)]

def report (or : Oracle) (gens : List Gen) (args : List Val) (trial seed : Nat) (o : Outcome)
    (s : HState Val) (ret : Option Val) : Json :=
  let inputs := (gens.zip args).flatMap (fun (g, v) => flatten g v)
  let memory := memoryOf or gens args
  let havoc := s.log.toList.map fun (site, iter, v) =>
    Json.mkObj [("idx", site), ("iter", iter), ("value", renderVal v)]
  Json.mkObj [("found", true), ("trial", trial), ("seed", seed),
    ("inputs", Json.mkObj inputs), ("memory", Json.mkObj memory), ("havoc", Json.arr havoc.toArray),
    ("model_output", modelOutput o ret)]

/-- Run `trials` random trials; print one JSON line, `{"found": false, ...}` if nothing refutes. -/
def run (p : GoSSA.Prog) (prop : Property) (gens : List Gen) (extra : List Nat)
    (seed trials fuel : Nat) : IO Unit := do
  let mut g := mkStdGen seed
  let mut fuelOuts := 0
  for trial in [0:trials] do
    let (pfn, g') := randNat g 0 2
    g := g'
    let pf := Profile.ofNat pfn
    -- Memory and havoc are re-drawn every trial; the trial seed makes them reproducible.
    let tseed := (mixHash (UInt64.ofNat seed) (UInt64.ofNat trial)).toNat
    let or := oracleOf pf extra tseed
    let h := havocOf tseed p.havocWidths
    let (args, g') := genArgs pf extra g gens
    g := g'
    let (o, s, ret) := check p or h prop args fuel
    if o.refutes prop then
      IO.println (report or gens args trial seed o s ret).compress
      return
    if let .outOfFuel := o then fuelOuts := fuelOuts + 1
  IO.println (Json.mkObj [("found", false), ("trials", trials), ("out_of_fuel", fuelOuts)]).compress

-- Exhaustive engine ---------------------------------------------------------

/-- The value set the exhaustive engine enumerates for `w`-bit integers:
boundary values, mined literals and their neighbours, and magnitudes up to 8. -/
def smallInts (w : Nat) (extra : List Nat) : List (BitVec w) :=
  ((specials w ++ mined w extra).toList ++
    (List.range 17).map fun n => BitVec.ofInt w (Int.ofNat n - 8)).eraseDups

/-- All length-`n` tuples over `xs`. -/
def tuples (xs : List α) : Nat → List (List α)
  | 0 => [[]]
  | n + 1 => (tuples xs n).flatMap fun t => xs.map (· :: t)

/-- Enumerate one parameter. A slice comes with its contents (the memory
table for its `ref`). -/
def enumGen (extra : List Nat) (maxLen fresh : Nat) : Gen → List (Val × Option (List (BitVec 64)))
  | .int _ w _ => (smallInts w extra).map fun v => (.int w v, none)
  | .bool _ => [(.bool true, none), (.bool false, none)]
  | .slice _ w _ maxLen' =>
    let elems := (smallInts w extra).map (·.zeroExtend 64)
    (List.range (min maxLen maxLen' + 1)).flatMap fun len =>
      (tuples elems len).map fun t =>
        (.slice fresh 0#64 (BitVec.ofNat 64 len) (BitVec.ofNat 64 len), some t)

/-- Size of the domain `enumArgs` would produce, computed before building it. -/
def domainSize (extra : List Nat) (maxLen : Nat) : List Gen → Nat
  | [] => 1
  | g :: gs =>
    let here := match g with
      | .int _ w _ => (smallInts w extra).length
      | .bool _ => 2
      | .slice _ w _ maxLen' =>
        let k := (smallInts w extra).length
        (List.range (min maxLen maxLen' + 1)).foldl (fun acc len => acc + k ^ len) 0
    here * domainSize extra maxLen gs

/-- The full argument domain, each entry with the memory tables of its slices
in `ref` order. -/
def enumArgs (extra : List Nat) (maxLen : Nat) : List Gen → Nat → List (List Val × List (List (BitVec 64)))
  | [], _ => [([], [])]
  | g :: gs, fresh =>
    let here := enumGen extra maxLen fresh g
    let fresh' := match g with | .slice .. => fresh + 1 | _ => fresh
    let rest := enumArgs extra maxLen gs fresh'
    here.flatMap fun (v, tbl) => rest.map fun (vs, tbls) => (v :: vs, tbl.toList ++ tbls)

def tableOracle (tbls : List (List (BitVec 64))) : Oracle :=
  { mem := fun ref idx => ((tbls[ref]?).bind (·[idx.toNat]?)).getD 0#64 }

/-- Try every input in the small domain. Prints a counterexample line, or
`{"found": false, "exhaustive": true, ...}` when the whole domain holds, or
`"exhaustive": false` when the domain exceeds `limit`. Havoc sites (if any)
are not enumerated; they take seed-derived values. -/
def exhaust (p : GoSSA.Prog) (prop : Property) (gens : List Gen) (extra : List Nat)
    (maxLen limit fuel : Nat) : IO Unit := do
  let n := domainSize extra maxLen gens
  if n > limit then
    IO.println (Json.mkObj [("found", false), ("exhaustive", false),
      ("reason", "domain too large"), ("domain", n), ("limit", limit)]).compress
    return
  let h := havocOf 0 p.havocWidths
  let mut fuelOuts := 0
  let mut i := 0
  for (args, tbls) in enumArgs extra maxLen gens 0 do
    let or := tableOracle tbls
    let (o, s, ret) := check p or h prop args fuel
    if o.refutes prop then
      IO.println (report or gens args i 0 o s ret).compress
      return
    if let .outOfFuel := o then fuelOuts := fuelOuts + 1
    i := i + 1
  IO.println (Json.mkObj [("found", false), ("exhaustive", fuelOuts == 0), ("domain", n),
    ("out_of_fuel", fuelOuts), ("max_len", maxLen),
    ("havoc_sites", p.havocWidths.size)]).compress

end Go2Lean.Search
