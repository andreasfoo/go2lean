import Go2Lean.Dialect.GoSSA

/-! The concurrent machine (`docs/concurrency.md`, issue #2): goroutines,
channels, `select` and the `sync` primitives over the GoSSA dialect.

The big-step interpreter (`Go2Lean.SSA.runFunc`) stays for sequential code;
a function that can reach a synchronisation operation (`Func.sync`) runs
here. A goroutine is a stack of frames and runs from one scheduling point
(a `spawn` or `sync` terminator) to the next in one step; calls of functions
that cannot synchronise run to their end inside that step. At each step the
schedule (the havoc site `Prog.schedSite`) picks one of the goroutines that
can move. Channels live on the heap (`chanObj`); a mutex, read-write mutex,
wait group or once is a word in place of its field (any value but the ones
these operations store reads as the zero state).

A run ends when no goroutine can move: with the main goroutine's result when
every goroutine is done, `goroutineLeak` when the main goroutine returned
and another is blocked forever, `deadlock` when the main goroutine itself
is blocked. A goroutine blocked on a channel the main goroutine returns is
not a leak: the caller may still use the channel (`gen` returning `<-chan`). A panic in any goroutine ends the run with it, as in Go. -/

namespace Go2Lean.GoConc

open Go2Lean.SSA Go2Lean.GoSSA

abbrev Func := Go2Lean.GoSSA.Func
abbrev Prog := Go2Lean.GoSSA.Prog

/-- A call frame. `atTerm`: the block's instructions ran and its terminator
(a `spawn` or `sync`) waits. `dst`, `next`, `nargs`: where the caller
continues with this frame's result. -/
structure Frame where
  fn : Func
  bid : BlockId
  env : Env Val
  atTerm : Bool := false
  dst : Var := 0
  next : BlockId := 0
  nargs : List Var := []

instance : Inhabited Frame :=
  ⟨{ fn := { name := "", params := [], nvals := 0, entry := 0, blocks := #[] }, bid := 0, env := #[] }⟩

structure Gor where
  frames : List Frame
  done : Bool := false
  result : Val := .unit

instance : Inhabited Gor := ⟨{ frames := [] }⟩

/-- The frame a call of `fn` on `args` starts in. -/
def entryFrame (fn : Func) (args : List Val) : Frame :=
  { fn, bid := fn.entry, env := bindParams (Array.replicate fn.nvals default) fn.params args }

/-! ## One goroutine, up to its next scheduling point -/

inductive Stop where
  | atSync (frames : List Frame)
  | returned (v : Val)
  | panicked (k : PanicKind)
  | outOfFuel

/-- The frames after the top one returned `v`: the caller continues. -/
def returnTo (fr : Frame) (v : Val) : List Frame → Option (List Frame)
  | c :: rest =>
    let env := enter c.fn fr.next (c.env.set! fr.dst v) fr.nargs
    some ({ c with env := env, bid := fr.next, atTerm := false } :: rest)
  | [] => none

/-- Run the top frame's blocks until the goroutine reaches a `spawn` or
`sync`, returns from its first frame, or panics. -/
def advance (p : Prog) (or : Oracle) (h : Havoc Val) : List Frame → HState Val → Nat → Stop × HState Val
  | [], s, _ => (.panicked .modelStuck, s)
  | _, s, 0 => (.outOfFuel, s)
  | fr :: rest, s, fuel + 1 =>
    if fr.atTerm then (.atSync (fr :: rest), s) else
    match fr.fn.blocks[fr.bid]? with
    | none => (.panicked .modelStuck, s)
    | some blk =>
      match execInstrs (Op := Go2Lean.GoSSA.Op) or h blk.instrs.toList fr.env s with
      | (.panic k, s) => (.panicked k, s)
      | (.outOfFuel, s) => (.outOfFuel, s)
      | (.ok env, s) =>
        let goto := fun (b : BlockId) (env : Env Val) (args : List Var) =>
          { fr with bid := b, env := enter fr.fn b env args, atTerm := false } :: rest
        -- A call: a frame of its own for a function that can synchronise,
        -- else the big-step interpreter, as in `runBlock`.
        let call := fun (f : FuncId) (vals : List Val) (dst next : BlockId) (nargs : List Var) (fb : BlockId) =>
          match p.funcs[f]? with
          | none => (.panicked .modelStuck, s)
          | some callee =>
            if callee.sync then
              advance p or h ({ entryFrame callee vals with dst, next, nargs } :: { fr with env } :: rest) s fuel
            else
              match runFunc p or h f vals s fuel with
              | (.ok v, s') => advance p or h (goto next (env.set! dst v) nargs) s' fuel
              | (.panic k, s') =>
                if k.isStuck then
                  let s'' := { s' with heap := s.heap, stuck := s.stuck }
                  advance p or h (goto fb env []) (s''.assume (stuckCallNote p f)) fuel
                else (.panicked k, s')
              | (.outOfFuel, s') => (.outOfFuel, s')
        match blk.term with
        | .ret v =>
          match returnTo fr env[v]! rest with
          | some frames => advance p or h frames { s with exit := some (fr.fn.name, fr.bid) } fuel
          | none => (.returned env[v]!, { s with exit := some (fr.fn.name, fr.bid) })
        | .jump b args => advance p or h (goto b env args) s fuel
        | .br c t ta f fa =>
          match Dialect.toBool (Op := Go2Lean.GoSSA.Op) env[c]! with
          | some true => advance p or h (goto t env ta) s fuel
          | some false => advance p or h (goto f env fa) s fuel
          | none => (.panicked .modelStuck, s)
        | .panic k => (.panicked k, s)
        | .call f args dst next nargs fb => call f (args.map (env[·]!)) dst next nargs fb
        | .callv fv args dst next nargs fb =>
          match Dialect.callee (Op := Go2Lean.GoSSA.Op) env[fv]! with
          | .nil => (.panicked .nilDereference, s)
          | .unknown => advance p or h (goto fb env [])
              (s.assume "a call through a function value the model does not know: its effects are not modelled") fuel
          | .known f bound => call f (bound ++ args.map (env[·]!)) dst next nargs fb
        | .xcall _ _ _ _ _ _ fb =>
          advance p or h (goto fb env [])
            (s.assume "a call into a package not linked into the model: its effects are not modelled") fuel
        | .spawn .. | .sync .. => (.atSync ({ fr with env, atTerm := true } :: rest), s)

/-! ## Pending operations -/

/-- The `sync` terminator goroutine `g` waits at, with its argument values. -/
def pendingSync (g : Gor) : Option (SyncOp × List Val) :=
  match g.frames with
  | f :: _ =>
    if !f.atTerm then none else
    match f.fn.blocks[f.bid]? with
    | some blk =>
      match blk.term with
      | .sync op args _ _ => some (op, args.map (f.env[·]!))
      | _ => none
    | none => none
  | [] => none

/-- Complete the `spawn` or `sync` goroutine `g` waits at, with result `r`:
it moves on to the terminator's successor and can run again. -/
def complete (g : Gor) (r : Val) : Gor :=
  match g.frames with
  | f :: rest =>
    match f.fn.blocks[f.bid]? with
    | some blk =>
      match blk.term with
      | .sync _ _ dst next =>
        { g with frames := { f with env := enter f.fn next (f.env.set! dst r) [], bid := next, atTerm := false } :: rest }
      | .spawn _ _ next =>
        { g with frames := { f with env := enter f.fn next f.env [], bid := next, atTerm := false } :: rest }
      | _ => g
    | none => g
  | [] => g

def samePtr : Val → Val → Bool
  | .ptr r p, .ptr r' p' => r == r' && p == p'
  | _, _ => false

def valNat : Val → Nat
  | .int _ v => v.toNat
  | _ => 0

/-- The cases of a `select`: send flag, channel, value (unit for a receive). -/
def selectCases : List Bool → List Val → List (Bool × Val × Val)
  | true :: ss, ch :: v :: rest => (true, ch, v) :: selectCases ss rest
  | false :: ss, ch :: rest => (false, ch, .unit) :: selectCases ss rest
  | _, _ => []

/-- go/ssa's result of a `select`: (index, recvOk, the received value of each
receive case, zero for the ones not chosen). -/
def selectResult (heap : Heap Val) (cases : List (Bool × Val × Val)) (idx : Int) (ok : Bool)
    (got : Val) : Val :=
  let recvs := (cases.zipIdx).filterMap fun ((send, ch, _), i) =>
    if send then none else
    some (if (i : Int) = idx then got else
      match chanGet heap ch with | some (_, _, _, _, z) => z | none => .unit)
  .tuple (.int 64 (BitVec.ofInt 64 idx) :: .bool ok :: recvs)

/-- Goroutine `g` waits to receive on channel `ref`: the result its pending
operation gets for a received value `v`. -/
def recvWaiter (heap : Heap Val) (ref : Nat) (g : Gor) : Option (Val → Val) :=
  match pendingSync g with
  | some (.recv withOk, [ch]) =>
    match chanGet heap ch with
    | some (r, _, _, _, _) => if r == ref then some fun v => if withOk then .tuple [v, .bool true] else v else none
    | none => none
  | some (.select sends _, args) =>
    let cases := selectCases sends args
    match (cases.zipIdx).find? fun ((send, ch, _), _) =>
        !send && (match chanGet heap ch with | some (r, _, _, _, _) => r == ref | none => false) with
    | some (_, i) => some fun v => selectResult heap cases i true v
    | none => none
  | _ => none

/-- Goroutine `g` waits to send on channel `ref`: the value, and the result
its pending operation gets once the value is taken. -/
def sendWaiter (heap : Heap Val) (ref : Nat) (g : Gor) : Option (Val × Val) :=
  match pendingSync g with
  | some (.send, [ch, v]) =>
    match chanGet heap ch with
    | some (r, _, _, _, _) => if r == ref then some (v, .unit) else none
    | none => none
  | some (.select sends _, args) =>
    let cases := selectCases sends args
    match (cases.zipIdx).find? fun ((send, ch, _), _) =>
        send && (match chanGet heap ch with | some (r, _, _, _, _) => r == ref | none => false) with
    | some ((_, _, v), i) => some (v, selectResult heap cases i false .unit)
    | none => none
  | _ => none

/-- The first goroutine other than `i` that satisfies `w`. -/
def findOther {α : Type} (gs : Array Gor) (i : Nat) (w : Gor → Option α) : Option (Nat × α) :=
  (List.range gs.size).findSome? fun j =>
    if j == i then none else
    match gs[j]? with
    | some g => if g.done then none else (w g).map (j, ·)
    | none => none

/-- The outcome of trying goroutine `i`'s pending operation. -/
inductive Fire where
  | done (gs : Array Gor) (heap : Heap Val)
  | blocked
  | panic (k : PanicKind)

instance : Inhabited Fire := ⟨.blocked⟩

def setChan (heap : Heap Val) (ref : Nat) (cap : BitVec 64) (buf : List Val) (closed : Bool) (z : Val) : Heap Val :=
  heap.set! ref (chanObj cap buf closed z)

/-- A send of `v` on `ch` by goroutine `i`, completed with `res`. -/
def fireSend (heap : Heap Val) (gs : Array Gor) (i : Nat) (ch v res : Val) : Fire :=
  if chanIsNil ch then .blocked else
  match chanGet heap ch with
  | none => .panic .modelStuck
  | some (ref, cap, buf, closed, z) =>
    if closed then .panic .closedChannel
    else if cap.toNat > 0 then
      if buf.length < cap.toNat then
        .done (gs.modify i (complete · res)) (setChan heap ref cap (buf ++ [v]) false z)
      else .blocked
    else
      match findOther gs i (recvWaiter heap ref) with
      | some (j, k) => .done ((gs.modify j (complete · (k v))).modify i (complete · res)) heap
      | none => .blocked

/-- A receive on `ch` by goroutine `i`; `res v ok` is its result. -/
def fireRecv (heap : Heap Val) (gs : Array Gor) (i : Nat) (ch : Val) (res : Val → Bool → Val) : Fire :=
  if chanIsNil ch then .blocked else
  match chanGet heap ch with
  | none => .panic .modelStuck
  | some (ref, cap, buf, closed, z) =>
    match buf with
    | v :: rest => .done (gs.modify i (complete · (res v true))) (setChan heap ref cap rest closed z)
    | [] =>
      match (if cap.toNat = 0 then findOther gs i (sendWaiter heap ref) else none) with
      | some (j, (v, sres)) => .done ((gs.modify j (complete · sres)).modify i (complete · (res v true))) heap
      | none => if closed then .done (gs.modify i (complete · (res z false))) heap else .blocked

/-- The lock word at `ptr`: any value these operations did not store is the
zero state. -/
def wordAt (heap : Heap Val) (ptr : Val) : Nat :=
  match heapLoad heap ptr with
  | .ok (.int _ v) => v.toNat
  | _ => 0

def setWord (heap : Heap Val) (ptr : Val) (n : Int) : Option (Heap Val) :=
  match heapStore heap ptr (.int 64 (BitVec.ofInt 64 n)) with
  | .ok heap => some heap
  | _ => none

/-- A read-write mutex: (readers, writer). -/
def rwAt (heap : Heap Val) (ptr : Val) : Nat × Bool :=
  match heapLoad heap ptr with
  | .ok (.tuple [.int _ r, .bool w]) => (r.toNat, w)
  | _ => (0, false)

def setRW (heap : Heap Val) (ptr : Val) (r : Nat) (w : Bool) : Option (Heap Val) :=
  match heapStore heap ptr (.tuple [.int 64 (BitVec.ofNat 64 r), .bool w]) with
  | .ok heap => some heap
  | _ => none

/-- A goroutine other than `i` waits for the writer side of `ptr`. -/
def writerWaiting (gs : Array Gor) (i : Nat) (ptr : Val) : Bool :=
  (findOther gs i fun g => match pendingSync g with
    | some (.wLock, [q]) => if samePtr q ptr then some () else none
    | _ => none).isSome

/-- Try goroutine `i`'s pending operation; `choice` picks among ready
`select` cases. -/
def fire (heap : Heap Val) (gs : Array Gor) (i : Nat) (choice : Nat) : Fire :=
  let done1 := fun (r : Val) (heap : Heap Val) => Fire.done (gs.modify i (complete · r)) heap
  let withWord := fun (o : Option (Heap Val)) (r : Val) => match o with
    | some heap => done1 r heap
    | none => Fire.panic .modelStuck
  match gs[i]? >>= pendingSync with
  | none => .blocked
  | some (op, args) =>
    match op, args with
    | .send, [ch, v] => fireSend heap gs i ch v .unit
    | .recv withOk, [ch] => fireRecv heap gs i ch fun v ok => if withOk then .tuple [v, .bool ok] else v
    | .close, [ch] =>
      if chanIsNil ch then .panic .closedChannel else
      match chanGet heap ch with
      | none => .panic .modelStuck
      | some (ref, cap, buf, closed, z) =>
        if closed then .panic .closedChannel else done1 .unit (setChan heap ref cap buf true z)
    | .select sends blocking, args =>
      let cases := selectCases sends args
      let tries := (cases.zipIdx).filterMap fun ((send, ch, v), k) =>
        let r := if send then fireSend heap gs i ch v (selectResult heap cases k false .unit)
          else fireRecv heap gs i ch fun x ok => selectResult heap cases k ok x
        match r with
        | .blocked => none
        | r => some r
      match tries with
      | [] => if blocking then .blocked else done1 (selectResult heap cases (-1) false .unit) heap
      | _ => tries[choice % tries.length]!
    | .lock, [ptr] => if wordAt heap ptr = 1 then .blocked else withWord (setWord heap ptr 1) .unit
    | .tryLock, [ptr] =>
      if wordAt heap ptr = 1 then done1 (.bool false) heap else withWord (setWord heap ptr 1) (.bool true)
    | .unlock, [ptr] => if wordAt heap ptr = 1 then withWord (setWord heap ptr 0) .unit else .panic .syncFatal
    | .wLock, [ptr] =>
      let (r, w) := rwAt heap ptr
      if r = 0 && !w then withWord (setRW heap ptr 0 true) .unit else .blocked
    | .wUnlock, [ptr] =>
      let (r, w) := rwAt heap ptr
      if w then withWord (setRW heap ptr r false) .unit else .panic .syncFatal
    | .rLock, [ptr] =>
      let (r, w) := rwAt heap ptr
      -- Writer preference: a waiting writer stops new readers, as in Go.
      if !w && !writerWaiting gs i ptr then withWord (setRW heap ptr (r + 1) false) .unit else .blocked
    | .rUnlock, [ptr] =>
      let (r, w) := rwAt heap ptr
      if r > 0 then withWord (setRW heap ptr (r - 1) w) .unit else .panic .syncFatal
    | .wgAdd, [ptr, .int _ d] =>
      let n := (wordAt heap ptr : Int) + d.toInt
      if n < 0 then .panic .explicit else withWord (setWord heap ptr n) .unit
    | .wgWait, [ptr] => if wordAt heap ptr = 0 then done1 .unit heap else .blocked
    | .onceEnter, [ptr] =>
      match wordAt heap ptr with
      | 0 => withWord (setWord heap ptr 1) (.bool true)
      | 1 => .blocked
      | _ => done1 (.bool false) heap
    | .yield, _ => done1 .unit heap
    | _, _ => .panic .modelStuck

/-! ## The scheduler -/

/-- Can goroutine `i` move? -/
def ready (heap : Heap Val) (gs : Array Gor) (i : Nat) : Bool :=
  match gs[i]? with
  | none => false
  | some g =>
    if g.done then false else
    match g.frames with
    | f :: _ =>
      if !f.atTerm then true else
      match f.fn.blocks[f.bid]? with
      | some blk =>
        match blk.term with
        | .spawn .. => true
        | .sync .. => match fire heap gs i 0 with | .blocked => false | _ => true
        | _ => false
      | none => false
    | [] => false

/-- Value `v` holds channel `ch`, itself or in a field (to depth `n`). -/
def reaches (ch : Val) : Nat → Val → Bool
  | 0, _ => false
  | n + 1, v => samePtr v ch || match v with
    | .structv fs | .tuple fs => fs.any (reaches ch n)
    | .boxed _ w => reaches ch n w
    | _ => false

/-- Goroutine `g` waits on a channel the main goroutine returned in `res`. -/
def waitsOnResult (res : Val) (g : Gor) : Bool :=
  match pendingSync g with
  | some (.send, ch :: _) | some (.recv _, ch :: _) => reaches ch 4 res
  | some (.select ss _, args) => (selectCases ss args).any fun (_, ch, _) => reaches ch 4 res
  | _ => false

/-- Where the goroutines that cannot move wait, for a deadlock or leak report. -/
def describeBlocked (gs : Array Gor) : String :=
  let parts := (gs.toList.zipIdx).filterMap fun (g, i) =>
    if g.done then none else
    match g.frames with
    | f :: _ =>
      let op := match pendingSync g with
        | some (op, _) => (s!"{repr op}".take 40).toString
        | none => "?"
      some s!"goroutine {i} at {f.fn.name}#{f.bid}: {op}"
    | [] => none
  String.intercalate "; " parts

def schedChoice (p : Prog) (h : Havoc Val) (s : HState Val) : Nat × HState Val :=
  match p.schedSite with
  | some site => let (v, s) := s.take h site; (valNat v, s)
  | none => (0, s)

/-- Run goroutine `i` (now able to move) to its next scheduling point. -/
def stepGor (p : Prog) (or : Oracle) (h : Havoc Val) (gs : Array Gor) (i : Nat) (s : HState Val)
    (fuel : Nat) : Except PanicKind (Array Gor × HState Val) × HState Val :=
  -- First the pending terminator, if any.
  let started : Except PanicKind (Array Gor × HState Val) :=
    match gs[i]? with
    | none => .error .modelStuck
    | some g =>
      match g.frames with
      | f :: _ =>
        if !f.atTerm then .ok (gs, s) else
        match f.fn.blocks[f.bid]? with
        | some blk =>
          match blk.term with
          | .spawn fv args _ =>
            let gs := gs.modify i (complete · .unit)
            match Dialect.callee (Op := Go2Lean.GoSSA.Op) f.env[fv]! with
            | .nil => .error .nilDereference
            | .unknown => .ok (gs, s.assume "a goroutine running a function value the model does not know is not modelled")
            | .known fid bound =>
              match p.funcs[fid]? with
              | some callee => .ok (gs.push { frames := [entryFrame callee (bound ++ args.map (f.env[·]!))] }, s)
              | none => .error .modelStuck
          | .sync .. =>
            let (c, s) := schedChoice p h s
            match fire s.heap gs i c with
            | .done gs heap => .ok (gs, { s with heap })
            | .blocked => .error .modelStuck
            | .panic k => .error k
          | _ => .error .modelStuck
        | none => .error .modelStuck
      | [] => .error .modelStuck
  match started with
  | .error .modelStuck =>
    -- Say which operation: a channel or lock word the model does not know.
    let note := match gs[i]? >>= pendingSync with
      | some (op, args) => (s!"{repr op} on {repr args}".take 200).toString
      | none => "a go statement"
    (.error .modelStuck, { s with stuck := s.stuck <|> some note })
  | .error k => (.error k, s)
  | .ok (gs, s) =>
    match gs[i]? with
    | none => (.error .modelStuck, s)
    | some g =>
      match advance p or h g.frames s fuel with
      | (.atSync frames, s) => (.ok (gs.set! i { g with frames }, s), s)
      | (.returned v, s) => (.ok (gs.set! i { g with frames := [], done := true, result := v }, s), s)
      | (.panicked k, s) => (.error k, s)
      | (.outOfFuel, s) => (.error .other, { s with stuck := s.stuck <|> some "out of fuel in a goroutine" })

/-- Schedule until no goroutine can move. `outOfFuel` when the steps run out
(an unfair schedule spinning one goroutine ends here, not as a finding). -/
def schedule (p : Prog) (or : Oracle) (h : Havoc Val) (stepFuel : Nat) :
    Array Gor → HState Val → Nat → Res Val × HState Val
  | _, s, 0 => (.outOfFuel, s)
  | gs, s, n + 1 =>
    let cands := (List.range gs.size).filter (ready s.heap gs)
    match cands with
    | [] =>
      match gs[0]? with
      | some g0 =>
        if g0.done then
          if gs.all (fun g => g.done || waitsOnResult g0.result g) then (.ok g0.result, s)
          else (.panic .goroutineLeak, { s with panicAt := some (describeBlocked gs) })
        else (.panic .deadlock, { s with panicAt := some (describeBlocked gs) })
      | none => (.panic .modelStuck, s)
    | _ =>
      let (c, s) := schedChoice p h s
      let i := cands[c % cands.length]!
      match stepGor p or h gs i s stepFuel with
      | (.ok (gs, s), _) => schedule p or h stepFuel gs s n
      | (.error .other, s) => (.outOfFuel, s)
      | (.error k, s) => (.panic k, s)

/-- Run function `fid` on `args` in the concurrent machine: the main
goroutine and every goroutine it starts. -/
def runConc (p : Prog) (or : Oracle) (h : Havoc Val) (fid : FuncId) (args : List Val) (fuel : Nat)
    (heap : Heap Val := #[]) : Res Val × HState Val :=
  match p.funcs[fid]? with
  | none => (.panic .modelStuck, { heap })
  | some fn => schedule p or h fuel #[{ frames := [entryFrame fn args] }] { heap } fuel

/-- Run `fid`: in the concurrent machine when it can synchronise, else with
the big-step interpreter. -/
def runAny (p : Prog) (or : Oracle) (h : Havoc Val) (fid : FuncId) (args : List Val) (fuel : Nat)
    (heap : Heap Val := #[]) : Res Val × HState Val :=
  match p.funcs[fid]? with
  | some fn => if fn.sync then runConc p or h fid args fuel heap else run p or h fid args fuel heap
  | none => run p or h fid args fuel heap

end Go2Lean.GoConc
