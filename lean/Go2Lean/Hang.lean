import Go2Lean.Dialect.GoSSA

/-!
# Definite infinite loops (#86)

A run that ends `outOfFuel` is no verdict: the model took more steps than
its fuel. `lasso` reruns it in a copy of the sequential interpreter
(`SSA.runBlock`) that also watches the state at each back edge (a jump to a
block not after the current one). A loop that comes back to a block with
the same environment, heap and havoc counters loops forever: the run from
there is deterministic (the oracle and the havoc function are fixed, and a
havoc read moves its site's counter). Brent's cycle detection keeps one
saved state per activation, so each back edge costs one comparison.

The interpreter is separate from `SSA.runBlock`, which the proofs unfold;
nothing here is used by them.
-/

namespace Go2Lean.Hang

open Go2Lean.SSA Go2Lean.GoSSA

partial def valEq : Val → Val → Bool
  | .int w a, .int w' b => w == w' && a.toNat == b.toNat
  | .bool a, .bool b => a == b
  | .slice r o l c, .slice r' o' l' c' => r == r' && o == o' && l == l' && c == c'
  | .tuple a, .tuple b => listEq a b
  | .err a, .err b => a == b
  | .ptr r p, .ptr r' p' => r == r' && p == p'
  | .structv a, .structv b => listEq a b
  | .arr a, .arr b => listEq a b
  | .str r o l, .str r' o' l' => r == r' && o == o' && l == l'
  | .boxed t v, .boxed t' v' => t == t' && valEq v v'
  | .opaqueNil, .opaqueNil => true
  | .unit, .unit => true
  | _, _ => false
where
  listEq : List Val → List Val → Bool
    | [], [] => true
    | a :: as, b :: bs => valEq a b && listEq as bs
    | _, _ => false

def arrEq (a b : Array Val) : Bool := a.isEqv b valEq

/-- The heap is often the same object at both visits (a loop that does
not write memory): compare addresses before contents. -/
private unsafe def heapEqUnsafe (a b : Heap Val) : Bool :=
  ptrAddrUnsafe a == ptrAddrUnsafe b || arrEq a b

@[implemented_by heapEqUnsafe]
def heapEq (a b : Heap Val) : Bool := arrEq a b

/-- Havoc counters, compared up to trailing zeros (a site first read
pads the array). -/
def countsEq (a b : Array Nat) : Bool :=
  let n := max a.size b.size
  (List.range n).all fun i => a[i]?.getD 0 == b[i]?.getD 0

/-- Brent's cycle detection for one activation: the state saved at the
last power of two, and the back edges since. -/
structure Brent where
  saved : Option (BlockId × Env Val × Heap Val × Array Nat) := none
  power : Nat := 1
  lam : Nat := 0

/-- Where a run loops forever: the function and block it comes back to,
and whether the run had taken an under-approximation by then (the loop
may then be the model's, not the program's). -/
structure Loop where
  site : String
  assumed : Bool

/-- At a back edge to block `b`: the loop if this state was seen, else
the updated detector. -/
def Brent.step (br : Brent) (fn : Func) (b : BlockId) (env : Env Val) (s : HState Val) : Except Loop Brent :=
  match br.saved with
  | some (b0, e0, h0, c0) =>
    if b0 == b && arrEq e0 env && heapEq h0 s.heap && countsEq c0 s.counts then
      .error { site := s!"@{fn.name}#{b}", assumed := !s.assumed.isEmpty }
    else next
  | none => next
where
  next :=
    let lam := br.lam + 1
    if lam == br.power then .ok { saved := some (b, env, s.heap, s.counts), power := br.power * 2, lam := 0 }
    else .ok { br with lam }

abbrev R := Except Loop (Res Val × HState Val)

mutual

/-- `SSA.runBlock` without tracking, watching back edges. -/
partial def runBlock (p : Prog) (or : Oracle) (h : Havoc Val) (fn : Func) (bid : BlockId)
    (env : Env Val) (s : HState Val) (br : Brent) : (fuel : Nat) → R
  | 0 => .ok (.outOfFuel, s)
  | fuel + 1 =>
    match fn.blocks[bid]? with
    | none => .ok (.panic .modelStuck, s)
    | some blk =>
      match execInstrs or h false blk.instrs.toList env s with
      | (.panic k, s) => .ok (.panic k, s)
      | (.outOfFuel, s) => .ok (.outOfFuel, s)
      | (.ok env, s) =>
        let goto (b : BlockId) (env : Env Val) (s : HState Val) : R :=
          if b ≤ bid then
            match br.step fn b env s with
            | .error l => .error l
            | .ok br => runBlock p or h fn b env s br fuel
          else runBlock p or h fn b env s br fuel
        let callThen (f : FuncId) (args : List Val) (dst next fb : BlockId) (nargs : List Var) : R :=
          match runFunc p or h f args s fuel with
          | .error l => .error l
          | .ok (.ok v, s') =>
            let env := env.set! dst v
            goto next (enter fn next env nargs) s'
          | .ok (.panic k, s') =>
            if k.isStuck then
              let s'' := { s' with heap := s.heap, stuck := s.stuck }
              goto fb (enter fn fb env []) (s''.assume (stuckCallNote p f))
            else .ok (.panic k, s')
          | .ok (.outOfFuel, s') => .ok (.outOfFuel, s')
        match blk.term with
        | .ret v => .ok (.ok env[v]!, s)
        | .panic k => .ok (.panic k, s)
        | .jump b args => goto b (enter fn b env args) s
        | .br c t targs f fargs =>
          match Dialect.toBool (Op := Op) env[c]! with
          | some true => goto t (enter fn t env targs) s
          | some false => goto f (enter fn f env fargs) s
          | none => .ok (.panic .modelStuck, s)
        | .call f args dst next nargs fb => callThen f (args.map (env[·]!)) dst next fb nargs
        | .callv fv args dst next nargs fb =>
          match Dialect.callee (Op := Op) env[fv]! with
          | .nil => .ok (.panic .nilDereference, s)
          | .unknown => goto fb (enter fn fb env []) (s.assume "a call through a function value the model does not know: its effects are not modelled")
          | .known f bound => callThen f (bound ++ args.map (env[·]!)) dst next fb nargs
        | .spawn .. | .sync .. => .ok (.panic .modelStuck, s)
        | .xcall _ _ _ _ _ _ fb =>
          goto fb (enter fn fb env []) (s.assume "a call into a package not linked into the model: its effects are not modelled")

partial def runFunc (p : Prog) (or : Oracle) (h : Havoc Val) (fid : FuncId) (args : List Val)
    (s : HState Val) : (fuel : Nat) → R
  | 0 => .ok (.outOfFuel, s)
  | fuel + 1 =>
    match p.funcs[fid]? with
    | none => .ok (.panic .modelStuck, s)
    | some fn =>
      let env := bindParams (Array.replicate fn.nvals default) fn.params args
      runBlock p or h fn fn.entry env s {} fuel

end

/-- Rerun function `fid` on `args` as `SSA.run` does: the loop it never
leaves, if it comes back to a state. -/
def lasso (p : Prog) (or : Oracle) (h : Havoc Val) (fid : FuncId) (args : List Val) (fuel : Nat)
    (heap : Heap Val := #[]) : Option Loop :=
  match runFunc p or h fid args { heap := heap } fuel with
  | .error l => some l
  | .ok _ => none

end Go2Lean.Hang
