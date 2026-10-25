## LevenshteinDistance_nopanic-round-6

The theorem as stated is false in the model, so no proof of it exists. I checked this by running the model on a concrete input.

I ran `runFunc prog … 4` with `s` = 1030 ASCII bytes and `t` empty. The state is `{}` with the two heap objects added. Every hypothesis of the theorem holds for it, and the run ends in a panic (`modelStuck`). With 100 bytes it returns normally.

The cause is block 2, which appends one rune per iteration to a slice that starts with capacity 64. The model's `appendSlice` doubles the capacity on each overflow. It panics with `modelStuck` once the new capacity would exceed 1024. That happens for any `s` with 1024 or more runes. `wpFun` counts any panic as `False`.

The blocks below can't prove the theorem. The proof block deliberately ends in `fail`, so it won't compile. I left out `sorry` because it's banned. The statement needs a hypothesis like `s_.length < 1024`, or a postcondition that allows `modelStuck`.

-- No helper lemmas: the theorem as stated is false in the model.
-- Counterexample: s = 1030 ASCII bytes, t = []; runFunc panics (modelStuck)
-- because appendSlice would grow the slice past its 1024 capacity limit.


## MatchNormalized_nopanic-round-6

I couldn't prove this theorem, because as stated it is false. The proof below does not compile.

`MatchNormalized` (f7) calls `normalizeTransformer` (f29) first. f29's entry block runs `.assume "normalizeTransformer: load Mn: …"`. `HState.assume` pushes that note whenever `s.assumed` is empty, and the hypothesis `h_assumed` guarantees it is.

I ran f7 on concrete inputs with `#eval`. It returns `ok` and `s.assumed` holds three notes: the f29 one, plus notes from `stringTransform` and `matchTransformed`. So `wpFun` at a large enough fuel demands `s'.assumed = #[]` of a run whose final state has it non-empty. The hypotheses (`h_source`, `h_target`, `h_assumed`) don't change that, since f29's note doesn't depend on the state. The goal can't be closed for any proof.

Fixing it means changing the statement. Either drop the `s'.assumed = #[]` conjunct, or have go2lean not emit a no-under-approximation contract for functions that call the partial model f29. I'm not allowed to restate the theorem, so I'm leaving the proof as an honest attempt that stops where the goal becomes false.


  apply wpFun_of_wpPt prog_f7
  apply wpPt_of_block prog_f7 rfl
  intro k

## MatchNormalizedFold_nopanic-round-6

I can't prove this theorem, because it is false as stated. The proof block below is only the opening step, and it will not compile as a full proof.

The postcondition `s'.assumed = #[]` cannot hold for `MatchNormalizedFold`. Its first block calls `normalizedFoldTransformer` (f30). That calls `normalizeTransformer` (f29), whose entry block runs `.assume "normalizeTransformer: load Mn: …"` unconditionally. `HState.assume` pushes that note onto `assumed`, and nothing ever removes it. For any fuel large enough to finish, one of two things happens:

- The run returns `.ok` with `assumed` non-empty, so the postcondition fails.
- The run panics (for example, a stuck callee), and `wpFun` is `False` on a panic.

Fuel that is too small only gives `outOfFuel`, which is vacuously true, but `wpFun` quantifies over all fuel. So the contract is unprovable.

I checked this by evaluating the model with `lean_run_code` on a concrete empty state:

runFunc prog ⟨fun _ _ => 0⟩ (fun _ _ => default) false 29 [] {} 100

## RankMatch_nopanic-round-6

I can't prove this theorem, because it is false. I checked a counterexample by running the model, so no valid proof exists.

**Counterexample.** `RankMatch` calls `rank` (`f31`). Take `source = "a"` and `target = "ab"`. The heap is `#[unit, .arr [int8 97], .arr [int8 97, int8 98]]`, with `source_ref = 1`, `target_ref = 2` and both lengths matching, so every hypothesis holds. Take `hv := fun _ _ => .int 64 100`.

1. The length difference is 1, so `rank` skips the early returns and enters the nested rune loops.
2. The first rune of `target` equals the first rune of `source`, so control reaches `rank` block 12. That block does `havoc 38` (the rune width, which `hv` can set to any value), then `add`, `strLen` and `strSlice 3 (i+size) len`.
3. With 100 as the width, `strSlice` has `lo = 100 > hi = 2`. It panics with `sliceBounds`.
4. I ran `runFunc prog ⟨fun _ _ => 0⟩ hv false 13 [.str 1 0 1, .str 2 0 2] s0 200`. It returned `panic sliceBounds (@rank#12: strSlice)`. With `hv := fun _ _ => .int 64 1` it returns `ok 1` with `assumed = #[]`.

`wpFun` treats any panic as `False`, and the theorem quantifies over every `hv`, so the statement fails for this `hv`. Block 12 stands in for Go's `target = target[i+utf8.RuneLen(r2):]`. The model leaves the rune length as an unconstrained havoc, and the generated statement doesn't restrict it.

The only fixes are on go2lean's side: constrain that havoc to the real rune length, or weaken the contract so the `strSlice` panic isn't ruled out. The blocks below are my partial attempt up to the point where the proof gets stuck. They contain no `sorry` and will not compile as a complete proof.

## RankMatchFold_nopanic-round-6

I couldn't prove this theorem, because it is false as stated. I'm not giving a proof: any block I wrote would either fail to compile or need `sorry`.

**Counterexample.** `RankMatchFold` (f14) calls `foldTransformer` (f22) and then `rank` (f31). `rank` calls `stringTransform` (f33) on both strings. The transformer it gets is `.boxed 1974785526 …`, so the `unbox 3769207349` in f33 block 5 fails its tag check. Control then goes to f33 block 2, which `havoc`s three values at sites 44, 45 and 46. Block 2 then returns the havoc'd site-44 value as the transformed string, as long as the site-46 value equals `.err false`.

The theorem quantifies over every `hv`, so `hv` may return something that isn't a string at site 44. I tried `hv 46 _ = .err false` and `hv 44 _ = .int 64 0`, with every other site returning `.unit`. The next `strLen` in `rank` (block 16) then gets stuck. The interpreter takes the call-fallback and records the assumption "the model got stuck in rank: its result is havoc, its effects are dropped". I ran this with `#eval`:

runFunc prog ⟨fun _ _ => 0⟩ hvb false 14 [.str 1 0 1, .str 1 0 1] st 40
  = (.ok .unit, assumed = #[the model got stuck in rank: …])

Here `st.heap = #[.unit, .arr [.int 8 97]]`, so both arguments are `.str 1 0 1` over the one-byte array `[97]`. This state satisfies every hypothesis: lengths match, refs are nonzero, the heap holds both strings, and `assumed = #[]`.

The postcondition `s'.assumed = #[]` fails on that run, so `wpFun … (fun ret s' => s'.assumed = #[] ∧ True) s` is false for that `orc`, `hv` and `s`. I tried to formalise this as `¬ wpFun …` with `decide +kernel`, but it timed out, so only the `#eval` backs the claim.

## RankMatchNormalized_nopanic-round-6

I couldn't prove this theorem, and I believe it is false as stated. The two blocks below are not a working proof.

**Counterexample.** Evaluating `runFunc prog orc0 hv0 false 15 [...] st0 100` in the model gives `ok` with `assumed` of size 3. The inputs are:
- `orc0 := ⟨fun _ _ => 0⟩`
- `hv0 := fun _ _ => default`
- `st0 := { heap := #[.arr [], .arr [.int 8 97]] }`, so `st0.assumed = #[]`
- arguments `[.str 1 0 1, .str 1 0 1]`, i.e. `source = target = [97]`, both refs 1, both lengths 1

These satisfy every hypothesis of the theorem. The postcondition `s'.assumed = #[]` fails on this run, and it holds for every fuel large enough to finish.

**Why it fails.**
- `f29` (`normalizeTransformer`), called in block 0 of `f15`, executes an `.assume "normalizeTransformer: load Mn: …"` instruction.

## RankMatchNormalizedFold_nopanic-round-6

I can't prove this theorem, because the statement is false. I could not make the proof check, so the proof block below is not a finished proof.

Running the model on concrete inputs shows the problem. `runFunc prog … 16 [.str 1 0 0, .str 1 0 0]` returns `.ok`, and the final state has 3 entries in `assumed`.

The path is this:
1. `RankMatchNormalizedFold` (f16) calls `normalizedFoldTransformer` (f30).
2. f30 calls `normalizeTransformer` (f29).
3. Block 0 of f29 runs `.assume "normalizeTransformer: load Mn: assumed a pointer to a fresh unicode.RangeTable…"` on every run.
4. f30 and f16 do not reset the state, so `s'.assumed` is never `#[]` when the run returns.

So the postcondition `s'.assumed = #[] ∧ True` fails for every run that returns. A run that panics would also fail, because `wpFun` counts a panic as `False`. The only way out is running out of fuel, and large enough fuel finishes the run. No invariant or lemma can change this.


## MatchFold_nopanic-round-6

I couldn't prove this theorem, because it is false as stated. I confirmed that by running the model. The proof block below is therefore a placeholder and will not compile; I did not use `sorry`.

**Counterexample.**
- **Hypotheses.** Take `source = target = []`, both lengths 0, `source_ref = 1`, `target_ref = 2`, and a state `s` whose heap is `#[Val.unit, Val.arr [], Val.arr []]`. All the theorem's hypotheses hold. Take any `orc` and `hv := fun _ _ => Val.unit`.
- **Run.** `runFunc prog orc hv false 6 [.str 1 0 0, .str 2 0 0] s 200` returns `ok Val.unit`. Its final state has `assumed = #["the model got stuck in stringTransform: its result is havoc, its effects are dropped", "the model got stuck in matchTransformed: its result is havoc, its effects are dropped"]`.
- **Why it breaks the postcondition.** In `stringTransform`, the havoc'd value is compared with `ne` against `err false`. In `matchTransformed`, the havoc'd value is passed to `add`. With a non-integer or non-error havoc value, the callee gets stuck. `runBlock` then follows the caller's fallback block and records an assumption. So `s'.assumed = #[]` fails, and no proof can exist.
- **Other havoc functions.** I also tried `hv := fun _ _ => Val.err false`. It still ends with a stuck `matchTransformed`.

The statement quantifies over every `hv` and allows any `Val`, which is more than the model's `havocWidths` suggest the havoc values are meant to be. The postcondition `s'.assumed = #[]` would need `hv` restricted to well-typed values, or the stuck-call fallbacks excluded.


  -- No valid proof exists: the stated theorem is false (see the counterexample above).

## ComputeDistance_nopanic-round-6

I couldn't prove this theorem, because it looks false as stated. I did not re-run the file. The blocks below are my previous attempt, copied unchanged, and Lean's only error on it was the branch described here.

**Where it breaks.** Block 10 of `ComputeDistance` does `.havoc 49 11` and then `.op 50 (.conv 64 false) [49]`. In the model:
- `HState.take` returns `hv site iter` unchanged. The interpreter never uses `havocWidths`.
- `hv` can return any `Val`.
- `denotePure` on `.conv` with a non-integer operand, for example `.unit`, gives `.panic .modelStuck`. I confirmed this for `.unit` with `rfl`.
- `wpFun` treats a panic as `False`.

**Why it is reachable.** Block 10 runs whenever the trimmed second rune slice has length at most 65535. That holds for ordinary inputs such as `a = "a"`, `b = "b"`. So an `hv` with `hv 11 0 = .unit` makes the run panic. I did not build that counterexample as a Lean proof.

**The leftover goal.** It is the `Res.panic modelStuck` arm of that `conv` match. It holds only if the havoc value is an `Val.int _ _`, which the statement does not assume. Every other path, including the `65535 < len` branch, goes through.


## Soundex_nopanic-round-6

The stated theorem is false, so no proof of it exists. The two blocks below are an empty prelude and a placeholder proof, and that proof does not close the goal. I did not use `sorry` or anything else forbidden.

**Why it fails.** Block 2 of Soundex runs `.assume "Soundex: call to (*strings.Builder).Grow ignored …"`. `HState.assume` pushes that note onto `s.assumed`, since the array is empty and its size is below 32. So any run through block 2, which is every non-empty input, ends with `assumed ≠ #[]`. The contract requires `s'.assumed = #[]`.

**Check.** I ran `runFunc prog ⟨fun _ _ => 0⟩ (fun _ _ => .unit) false 3 [.str 1 0 1]` on heap `#[.unit, .arr [.int 8 97#8]]` with fuel 200. It returns `.ok` with a non-empty `assumed`. That input satisfies every hypothesis of the theorem: `s__len = 1`, `s__ref = 1`, the heap entry matches `s_ = [97]`, and `s.assumed = #[]`. `decide +kernel` accepted this check. I also tried it as a Lean statement over arbitrary `orc` and `hv`, but `simp` ran out of steps before finishing. So the failure is shown for one oracle and havoc function, which is enough to refute the "for every" statement.

**What would be provable.** The theorem holds for the empty string, which returns at block 1 with no assumption. For non-empty input, the postcondition would have to drop `s'.assumed = #[]` or allow the Grow note.


  -- Not provable as stated: for s_ ≠ [] block 2's `.assume` (Builder.Grow ignored)
  -- makes s'.assumed ≠ #[]. Counterexample: s_ = [97], s__len = 1, s__ref = 1.
  intro fuel

## Ukkonen_nopanic-round-6

I couldn't prove this theorem, because it is false as stated. No proof exists, so the proof block below is incomplete and won't compile.

The function divides by `scost` or `icost` or `dcost`, and the statement puts no condition on them. With `icost = dcost = scost = 0` the control flow is:

- Block 0 (`icost < dcost`) is false, so it goes to block 3.
- Block 3 (`dcost < scost`) is false, so it goes to block 6, which jumps to block 2 with value 5 = `scost` = 0.
- Block 2 goes to block 7 or 8, which multiplies by value 5 and gets 0. Block 9 is entered with value 6 = 0.
- Block 9 evaluates `sdiv 6 5`, which is 0 / 0, and the model's `dp_sdiv` turns that into `.panic .divideByZero`.

`wpFun` maps a panic to `False`. All the hypotheses are satisfiable. For example, take `a = b = []`, `a_ref = b_ref = 1`, `a_len = b_len = 0` and `s.heap = #[.arr [], .arr []]`.

I checked this in Lean by running the model on that instance with fuel 20. The result is a panic, and `decide +kernel` accepts it:

## WagnerFischer_nopanic-round-6

I couldn't prove `WagnerFischer_nopanic`, because the statement is false in the model. The two blocks below are placeholders, and the proof block does not compile.

Why it's false:
- **Cause:** the model's `makeSlice` returns `modelStuck` when the capacity exceeds 1024. Its semantics are `if len.ule cap && cap.toNat ≤ 1024 then alloc else if len.ule cap then panic modelStuck else panic makeSliceLen`.
- **Block 0:** `WagnerFischer` computes `strLen b + 1` and calls `makeSlice` with that as both length and capacity. Whenever `b.length ≥ 1024`, the capacity is above 1024 and the model panics with `modelStuck` in the entry block.
- **Fuel:** two units of fuel are enough to reach that panic. The `wpFun` unfolding turns it into `False`.
- **Hypotheses are satisfiable:** for example `a = []`, `b = List.replicate 1100 0`, `a_len = 0`, `b_len = 1100`, a heap holding both arrays, `a_ref = 1`, `b_ref = 2` and `s.assumed = #[]`. So no proof of the stated theorem can exist.
- **Not machine-checked:** the formal disproof (`WF_notWF`) from my previous attempt, which tried `rfl` on a concrete run, still hit heartbeat limits in the sandbox, and a fresh attempt timed out after 60 seconds. The argument rests on the `makeSlice` definition I printed from the model, not on a Lean-checked counterexample.

The statement would be provable with an extra hypothesis such as `b.length + 1 ≤ 1024` (and the loops' other bounds). I did not build that proof, because the statement is fixed and I may not change it.

-- No proof exists: the fixed statement is false for |b| ≥ 1024 (makeSlice caps at 1024).

