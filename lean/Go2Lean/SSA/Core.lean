/-!
# SSA core

A dialect-independent SSA program representation with an executable,
fuel-bounded semantics. The design follows lean-mlir's split between a
generic SSA core and dialects, but keeps only what go2lean needs:

* basic blocks with block parameters (phi nodes are lowered to them by the
  frontends), straight-line instructions and a terminator;
* a dialect that supplies the value type, the instruction set and the
  semantics of a single instruction; dialects cannot branch or call;
* explicit `havoc` instructions for values the frontend chose not to model
  (loads, unmodeled calls). Their values are extra inputs to the program, so a
  counterexample search can pick them and report them.

Every recursion is structural on `fuel`, so the interpreter is total and
usable from `#eval`, `decide` and, after unfolding, `bv_decide`.
-/

namespace Go2Lean.SSA

/-- Go runtime panic categories. `tag` matches `contract.PanicKind` on the Go side. -/
inductive PanicKind where
  | indexOutOfRange
  | divideByZero
  | nilDereference
  | negativeShift
  | sliceBounds
  | makeSliceLen
  /-- A failed single-result type assertion ("interface conversion"). -/
  | typeAssertion
  | explicit
  | other
  /-- Not a Go panic: the model got stuck (ill-typed operands, missing block).
  Signals a frontend bug; M3 reports it as inconclusive. -/
  | modelStuck
  deriving Repr, DecidableEq, Inhabited

/-- A stuck model says nothing about the program: `modelStuck` is an
ill-formed model, `other` is what a rejected callee's stub raises. -/
def PanicKind.isStuck : PanicKind → Bool
  | .modelStuck | .other => true
  | _ => false

def PanicKind.tag : PanicKind → String
  | .indexOutOfRange => "index_out_of_range"
  | .divideByZero => "divide_by_zero"
  | .nilDereference => "nil_dereference"
  | .negativeShift => "negative_shift"
  | .sliceBounds => "slice_bounds"
  | .makeSliceLen => "make_slice_len"
  | .typeAssertion => "type_assertion"
  | .explicit => "explicit"
  | .other => "other"
  | .modelStuck => "model_stuck"

/-- Result of running a function. `outOfFuel` is an artefact of the bounded
semantics and never a real program behaviour. -/
inductive Res (α : Type) where
  | ok (v : α)
  | panic (k : PanicKind)
  | outOfFuel
  deriving Repr, Inhabited

abbrev Var := Nat
abbrev BlockId := Nat
abbrev FuncId := Nat

/-- The heap: objects by reference. Index 0 is reserved for `nil`. -/
abbrev Heap (Val : Type) := Array Val

/-- What a dialect must provide. `Val` is the universe of runtime values,
`Op` the instruction set, `denote` the meaning of one instruction applied to
its operands. `Oracle` is read-only ambient state the dialect may consult
(for GoSSA: the contents of memory), fixed for a whole run so that two reads
of the same location agree. `toBool` reads a branch condition. -/
class Dialect (Op : Type) (Val Oracle : outParam Type) where
  /-- One instruction: reads the oracle, may read and write the heap. -/
  denote : Oracle → Heap Val → Op → List Val → Res (Val × Heap Val)
  toBool : Val → Option Bool

inductive Instr (Op : Type) where
  /-- `dst := op args` -/
  | op (dst : Var) (o : Op) (args : List Var)
  /-- `dst := havoc site` — a fresh non-deterministic value for havoc site
  `site`. Sites are numbered per program; iterations are counted at run time. -/
  | havoc (dst : Var) (site : Nat)
  deriving Repr

inductive Term where
  | ret (v : Var)
  | jump (b : BlockId) (args : List Var)
  | br (cond : Var) (t : BlockId) (targs : List Var) (f : BlockId) (fargs : List Var)
  | panic (k : PanicKind)
  /-- `dst := call f args; jump next nargs`. When the callee gets stuck
  (a rejected stub, an op the model has no rule for) the block `fb` runs
  instead on the heap as it was before the call: it binds `dst` to a havoc
  value, so the caller continues as if the call were unmodelled. -/
  | call (f : FuncId) (args : List Var) (dst : Var) (next : BlockId) (nargs : List Var) (fb : BlockId)
  /-- A call into function `f` of the model registered as `pkg`; the driver
  links it into a `call` when it merges the models, so the interpreter only
  sees one when a package was not compiled alongside. -/
  | xcall (pkg : String) (f : FuncId) (args : List Var) (dst : Var) (next : BlockId) (nargs : List Var) (fb : BlockId)
  deriving Repr

structure Block (Op : Type) where
  /-- Variables bound by this block's parameters, in order. -/
  params : List Var
  instrs : Array (Instr Op)
  term : Term
  deriving Repr

structure Func (Op : Type) where
  name : String
  /-- Variables bound by the function parameters, in order. -/
  params : List Var
  /-- Number of variables; the environment is an array of this size. -/
  nvals : Nat
  entry : BlockId
  blocks : Array (Block Op)
  deriving Repr

structure Prog (Op : Type) where
  funcs : Array (Func Op)
  /-- Width hint per havoc site, consumed by the search driver to generate values. -/
  havocWidths : Array Nat := #[]
  deriving Repr

/-- Havoc oracle: site → iteration → value. -/
abbrev Havoc (Val : Type) := Nat → Nat → Val

/-- Run-time bookkeeping for havoc: how often each site fired and which
values were handed out, so a counterexample can report them. -/
structure HState (Val : Type) where
  counts : Array Nat := #[]
  log : Array (Nat × Nat × Val) := #[]
  /-- Objects allocated before or during the run; `heap[0]` is the nil slot. -/
  heap : Heap Val := #[]
  /-- The op (with its operands) at which the model got stuck, for the
  engines' diagnostics. -/
  stuck : Option String := none
  /-- The function and block of the last `ret` executed: after a spec ran,
  the spec's own return, so the engines can tell which `return` a trial
  left through (a guard's `return true` or the real check). -/
  exit : Option (String × Nat) := none

namespace HState

def take (h : Havoc Val) (s : HState Val) (site : Nat) : Val × HState Val :=
  let counts :=
    if site < s.counts.size then s.counts
    else s.counts ++ Array.replicate (site + 1 - s.counts.size) 0
  let iter := counts[site]!
  let v := h site iter
  (v, { s with counts := counts.set! site (iter + 1), log := s.log.push (site, iter, v) })

end HState

variable {Op Val Oracle : Type} [Dialect Op Val Oracle] [Inhabited Val] [Repr Op] [Repr Val]

abbrev Env (Val : Type) := Array Val

/-- Bind block/function parameters to argument values. -/
def bindParams (env : Env Val) : List Var → List Val → Env Val
  | p :: ps, v :: vs => bindParams (env.set! p v) ps vs
  | _, _ => env

/-- Execute the straight-line part of a block. -/
def execInstrs (or : Oracle) (h : Havoc Val) :
    List (Instr Op) → Env Val → HState Val → Res (Env Val) × HState Val
  | [], env, s => (.ok env, s)
  | .op dst o args :: rest, env, s =>
    let vals := args.map (env[·]!)
    match Dialect.denote or s.heap o vals with
    | .ok (v, heap) => execInstrs or h rest (env.set! dst v) { s with heap := heap }
    | .panic .modelStuck =>
      -- Record where (op and operand shapes, truncated) for the engines.
      let site := (s!"{repr o} {repr vals}".take 160).toString
      (.panic .modelStuck, { s with stuck := s.stuck <|> some site })
    | .panic k => (.panic k, s)
    | .outOfFuel => (.outOfFuel, s)
  | .havoc dst site :: rest, env, s =>
    let (v, s) := s.take h site
    execInstrs or h rest (env.set! dst v) s

/-- Bind the parameters of block `b` to the values of `args` in `env`. -/
def enter (fn : Func Op) (b : BlockId) (env : Env Val) (args : List Var) : Env Val :=
  match fn.blocks[b]? with
  | some blk => bindParams env blk.params (args.map (env[·]!))
  | none => env

mutual

/-- Run block `bid` of `fn` in environment `env`. Each block entry and each
call consumes one unit of fuel. -/
def runBlock (p : Prog Op) (or : Oracle) (h : Havoc Val) (fn : Func Op) (bid : BlockId)
    (env : Env Val) (s : HState Val) : (fuel : Nat) → Res Val × HState Val
  | 0 => (.outOfFuel, s)
  | fuel + 1 =>
    match fn.blocks[bid]? with
    | none => (.panic .modelStuck, s)
    | some blk =>
      match execInstrs or h blk.instrs.toList env s with
      | (.panic k, s) => (.panic k, s)
      | (.outOfFuel, s) => (.outOfFuel, s)
      | (.ok env, s) =>
        match blk.term with
        | .ret v => (.ok env[v]!, { s with exit := some (fn.name, bid) })
        | .panic k => (.panic k, s)
        | .jump b args =>
          runBlock p or h fn b (enter fn b env args) s fuel
        | .br c t targs f fargs =>
          match Dialect.toBool (Op := Op) env[c]! with
          | some true => runBlock p or h fn t (enter fn t env targs) s fuel
          | some false => runBlock p or h fn f (enter fn f env fargs) s fuel
          | none => (.panic .modelStuck, s)
        | .call f args dst next nargs fb =>
          match runFunc p or h f (args.map (env[·]!)) s fuel with
          | (.ok v, s') =>
            let env := env.set! dst v
            runBlock p or h fn next (enter fn next env nargs) s' fuel
          | (.panic k, s') =>
            if k.isStuck then
              -- The callee could not be followed: forget what it did to the
              -- heap and take the fallback, which havocs the result.
              runBlock p or h fn fb (enter fn fb env []) { s' with heap := s.heap, stuck := s.stuck } fuel
            else (.panic k, s')
          | (.outOfFuel, s') => (.outOfFuel, s')
        | .xcall _ _ _ _ _ _ fb =>
          -- Not linked (the package was not compiled alongside): unmodelled.
          runBlock p or h fn fb (enter fn fb env []) s fuel

/-- Run function `fid` on `args`. -/
def runFunc (p : Prog Op) (or : Oracle) (h : Havoc Val) (fid : FuncId) (args : List Val)
    (s : HState Val) : (fuel : Nat) → Res Val × HState Val
  | 0 => (.outOfFuel, s)
  | fuel + 1 =>
    match p.funcs[fid]? with
    | none => (.panic .modelStuck, s)
    | some fn =>
      let env := bindParams (Array.replicate fn.nvals default) fn.params args
      runBlock p or h fn fn.entry env s fuel

end

/-- Entry point: run a function with fresh havoc state and an initial heap
(the objects the arguments point to; `#[]` when there are none). -/
def run (p : Prog Op) (or : Oracle) (h : Havoc Val) (fid : FuncId) (args : List Val) (fuel : Nat)
    (heap : Heap Val := #[]) : Res Val × HState Val :=
  runFunc p or h fid args { heap := heap } fuel

end Go2Lean.SSA
