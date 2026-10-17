import Go2Lean.Search

/-! The native search driver: a JSON description of a search, written by
M2 (`internal/constraint`), decoded into the generators and run against a
model from the registry. Running the engines in a compiled executable
instead of `#eval` avoids interpreting the interpreter. -/

namespace Go2Lean.Search

open Lean

/-- Decode a generator; the shape mirrors `constraint.genJSON` on the Go side. -/
partial def genOfJson (j : Json) : Except String Gen := do
  let k ← j.getObjValAs? String "k"
  let name := (j.getObjValAs? String "name").toOption.getD ""
  match k with
  | "int" => return .int name (← j.getObjValAs? Nat "w") (← j.getObjValAs? Bool "signed")
  | "bool" => return .bool name
  | "err" => return .err name
  | "slice" =>
    return .slice name (← j.getObjValAs? Nat "w") (← j.getObjValAs? Bool "signed") (← j.getObjValAs? Nat "maxLen")
  | "sliceOf" => return .sliceOf name (← genOfJson (← j.getObjVal? "elem")) (← j.getObjValAs? Nat "maxLen")
  | "structv" => return .structv name (← (← j.getObjValAs? (Array Json) "fields").toList.mapM genOfJson)
  | "ptr" => return .ptr name (← genOfJson (← j.getObjVal? "pointee"))
  | "str" => return .str name (← j.getObjValAs? Nat "maxLen")
  | "nilptr" => return .nilptr name
  | "ref" => return .ref name (← j.getObjValAs? Nat "ty")
  | "opaque" => return .opaque name
  | "arrOf" => return .arrOf name (← genOfJson (← j.getObjVal? "elem")) (← j.getObjValAs? Nat "n")
  | other => throw s!"unknown generator kind {other}"

def propertyOfJson (j : Json) : Except String Property := do
  match ← j.getObjValAs? String "kind" with
  | "noPanic" => return .noPanic (← j.getObjValAs? Nat "f")
  | "postcondition" => return .postcondition (← j.getObjValAs? Nat "f") (← j.getObjValAs? Nat "spec")
  | other => throw s!"unknown property kind {other}"

/-- One search: everything `run` or `exhaust` takes, plus the model's key. -/
structure Spec where
  prog : String
  engine : String
  prop : Property
  gens : List Gen
  extra : List Nat
  strs : List String
  table : TypeTable
  allowNil : Bool
  seed : Nat
  trials : Nat
  fuel : Nat
  maxLen : Nat
  limit : Nat
  budgetMs : Nat

def specOfJson (j : Json) : Except String Spec := do
  let gens ← (← j.getObjValAs? (Array Json) "gens").toList.mapM genOfJson
  let table ← (← j.getObjValAs? (Array (Array Json)) "table").mapM fun fs => fs.toList.mapM genOfJson
  return {
    prog := ← j.getObjValAs? String "prog"
    engine := ← j.getObjValAs? String "engine"
    prop := ← propertyOfJson (← j.getObjVal? "prop")
    gens, table
    extra := ← j.getObjValAs? (List Nat) "extra"
    strs := ← j.getObjValAs? (List String) "strs"
    allowNil := ← j.getObjValAs? Bool "allowNil"
    seed := ← j.getObjValAs? Nat "seed"
    trials := ← j.getObjValAs? Nat "trials"
    fuel := ← j.getObjValAs? Nat "fuel"
    maxLen := ← j.getObjValAs? Nat "maxLen"
    limit := ← j.getObjValAs? Nat "limit"
    budgetMs := (j.getObjValAs? Nat "budgetMs").toOption.getD 0 }

def runSpec (p : GoSSA.Prog) (s : Spec) : IO UInt32 := do
  match s.engine with
  | "random" => run p s.prop s.gens s.extra s.seed s.trials s.fuel s.strs s.table s.allowNil s.budgetMs; return 0
  | "exhaustive" => exhaust p s.prop s.gens s.extra s.maxLen s.limit s.fuel s.strs s.table s.allowNil s.budgetMs; return 0
  | other => IO.eprintln s!"unknown engine {other}"; return 2

/-- Entry point of the `go2lean-search` executable: `go2lean-search spec.json`,
with the models in `registry`. -/
def driverMain (registry : List (String × GoSSA.Prog)) (args : List String) : IO UInt32 := do
  match args with
  | [path] =>
    let txt ← IO.FS.readFile path
    match Json.parse txt >>= specOfJson with
    | .error e => IO.eprintln s!"{path}: {e}"; return 2
    | .ok spec =>
      match registry.lookup spec.prog with
      | none => IO.eprintln s!"{path}: no model named {spec.prog} in the registry (recompile)"; return 3
      | some p => runSpec p spec
  | _ => IO.eprintln "usage: go2lean-search <spec.json>"; return 2

end Go2Lean.Search
