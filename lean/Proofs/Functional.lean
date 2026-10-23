import Std.Tactic.BVDecide
import Go2Lean.Proof.Simp
import Go2Lean.Proof.WP
import Go2Lean.Generated.gossa.github_com_andreasfoo_go2lean_examples_proofs

/-! Functional proofs over the model of `examples/proofs` (issue #1): each
theorem holds for every input, and its proof is checked by the kernel alone
(`#print axioms` lists no `Lean.ofReduceBool`). The model is the deep
embedding the search engines run; `Go2Lean.Proof.WP` reads a weakest
precondition off it. Check from a workspace, as `OobClampIdentity.lean`:

    go run ./cmd/go2lean compile -workspace out ./examples/proofs
    cd out/lean && lake lean ../../lean/Proofs/Functional.lean

Not part of the `Go2Lean` library build: the FuncIds below are the
generated model's. -/

open Go2Lean.SSA Go2Lean.GoSSA Go2Lean.GoSSA.WP
open Go2Lean.Generated.gossa.github_com_andreasfoo_go2lean_examples_proofs

set_option maxHeartbeats 1000000

namespace Go2Lean.Proofs.Functional

def or0 : Oracle := { mem := fun _ _ => 0#64 }
def h0 : Havoc Val := fun _ _ => .unit

/-- Partial correctness at a point of this package's model. -/
local notation "wp" => wpPt prog or0 h0
local notation "wpN" => wpPtN prog or0 h0

theorem prog_f0 : prog.funcs[0]? = some f0 := rfl
theorem prog_f2 : prog.funcs[2]? = some f2 := rfl
theorem prog_f3 : prog.funcs[3]? = some f3 := rfl

/-- Unfold the instructions and terminator of the block in the goal. -/
macro "wp_simp" : tactic => `(tactic| simp [wpBlockN, wpTermN, execInstrs, enter, bindParams,
    f0, f2, f3,
    Dialect.denote, Dialect.toBool, Dialect.assumption, Go2Lean.GoSSA.assumption, denote,
    Array.getElem!_eq_getD, Array.getD_eq_getD_getElem?, Array.getElem?_setIfInBounds,
    Array.size_setIfInBounds, Array.getElem?_replicate, Array.size_replicate])

/-! ## `Max2`: no loop -/

theorem max2_correct (a b : BitVec 64) :
    wp ⟨3, 0⟩ (fun v _ => ∃ m, v = .int 64 m ∧ b.sle m ∧ a.sle m)
      (bindParams (Array.replicate 3 default) [0, 1] [.int 64 a, .int 64 b]) {} := by
  apply wpPt_of_block prog_f3 rfl
  intro n
  wp_simp
  split
  · -- a < b: block 1 returns b
    cases n with
    | zero => exact wpPtN_zero prog_f3
    | succ n =>
      apply wpPtN_of_block prog_f3 rfl; wp_simp
      simp only [BitVec.sle, BitVec.slt, decide_eq_true_eq, Option.some.injEq] at *; grind
  · -- otherwise: block 2 returns a
    cases n with
    | zero => exact wpPtN_zero prog_f3
    | succ n =>
      apply wpPtN_of_block prog_f3 rfl; wp_simp
      simp only [BitVec.sle, BitVec.slt, decide_eq_true_eq, decide_eq_false_iff_not,
        Option.some.injEq] at *; grind
  · simp_all

/-! ## `CountUp`: one loop -/

/-- At the header (block 3): `i` (value 1) between 0 and `max n 0`, `n`
(value 0) unchanged. -/
def countInv (n : BitVec 64) (env : Env Val) (_ : HState Val) : Prop :=
  env.size = 6 ∧ env[0]? = some (.int 64 n) ∧
  ∃ i : BitVec 64, env[1]? = some (.int 64 i) ∧ 0 ≤ i.toInt ∧ i.toInt ≤ max n.toInt 0

theorem countUp_correct (n : BitVec 64) :
    wp ⟨0, 0⟩ (fun v _ => v = .int 64 (if n.toInt < 0 then 0 else n))
      (bindParams (Array.replicate 6 default) [0] [.int 64 n]) {} := by
  apply wpPt_of_block prog_f0 rfl
  intro k
  wp_simp
  apply wpPtN_of_wpPt
  apply wpPt_loop prog_f0 (countInv n)
  · intro m env s hI ih
    obtain ⟨hsz, h0, i, h1, hlo, hhi⟩ := hI
    obtain ⟨_, e0⟩ := Array.getElem?_eq_some_iff.mp h0
    obtain ⟨_, e1⟩ := Array.getElem?_eq_some_iff.mp h1
    apply wpPtN_of_block prog_f0 rfl
    wp_simp
    simp [e0, e1, hsz]
    cases hc : i.slt n
    · -- exit: block 2 returns i
      dsimp only
      cases m with
      | zero => exact wpPtN_zero prog_f0
      | succ m =>
        apply wpPtN_of_block prog_f0 rfl
        wp_simp
        simp [e1, hsz]
        simp only [BitVec.slt, decide_eq_false_iff_not, Int.not_lt] at hc
        split
        · apply BitVec.eq_of_toInt_eq; simp; omega
        · apply BitVec.eq_of_toInt_eq; omega
    · -- body: block 1 adds 1 and jumps back to the header
      dsimp only
      cases m with
      | zero => exact wpPtN_zero prog_f0
      | succ m =>
        apply wpPtN_of_block prog_f0 rfl
        wp_simp
        simp [e1, hsz]
        apply wpPtN_le (Nat.le_succ m)
        apply ih
        simp only [BitVec.slt, decide_eq_true_eq] at hc
        have hn : n.toInt < 2 ^ 63 := by have := @BitVec.toInt_lt 64 n; simpa using this
        refine ⟨by simp [hsz], by simp [hsz]; exact e0, i + 1, by simp [hsz], ?_, ?_⟩
        · rw [BitVec.toInt_add_of_not_saddOverflow] <;> simp [BitVec.saddOverflow] <;> omega
        · rw [BitVec.toInt_add_of_not_saddOverflow] <;> simp [BitVec.saddOverflow] <;> omega
  · refine ⟨by simp, by simp, 0, by simp, by simp, Int.le_max_right _ _⟩

/-! ## `Max`: a loop reading a slice -/

/-- At the header (block 3: `m` is value 2, `i` value 3): every element
before `i` is at most `m`; the slice and the heap object under it are
unchanged. -/
def maxInv (xs : List (BitVec 64)) (len : BitVec 64) (env : Env Val) (s : HState Val) : Prop :=
  env.size = 22 ∧ env[0]? = some (.slice 1 0 len len) ∧
  s.heap[1]? = some (.arr (xs.map (.int 64))) ∧
  ∃ m i : BitVec 64, env[2]? = some (.int 64 m) ∧ env[3]? = some (.int 64 i) ∧
    1 ≤ i.toInt ∧ i.toInt ≤ max len.toInt 1 ∧
    ∀ j (hj : j < xs.length), (j : Int) < i.toInt → xs[j].toInt ≤ m.toInt

abbrev maxPost (xs : List (BitVec 64)) : Val → HState Val → Prop :=
  fun v _ => ∃ m, v = .int 64 m ∧ ∀ x ∈ xs, x.toInt ≤ m.toInt

theorem max_step (xs : List (BitVec 64)) (len : BitVec 64) (hlen : len.toInt = xs.length)
    (hL : len.toInt < 2 ^ 63) :
    ∀ n env s, maxInv xs len env s →
      (∀ env' s', maxInv xs len env' s' → wpN n ⟨2, 3⟩ (maxPost xs) env' s') →
      wpN (n + 1) ⟨2, 3⟩ (maxPost xs) env s := by
  intro n env s hI ih
  obtain ⟨hsz, h0, hh, m, i, h2, h3, hi1, hi2, hall⟩ := hI
  obtain ⟨_, e0⟩ := Array.getElem?_eq_some_iff.mp h0
  obtain ⟨_, e2⟩ := Array.getElem?_eq_some_iff.mp h2
  obtain ⟨_, e3⟩ := Array.getElem?_eq_some_iff.mp h3
  apply wpPtN_of_block prog_f2 rfl
  wp_simp
  simp [e0, e3, hsz]
  cases hc : i.slt len
  · -- exit: i ≥ len, return m
    simp only [BitVec.slt, decide_eq_false_iff_not, Int.not_lt] at hc
    dsimp only
    cases n with
    | zero => exact wpPtN_zero prog_f2
    | succ n =>
      apply wpPtN_of_block prog_f2 rfl
      wp_simp
      simp [e2, hsz, maxPost]
      intro x hx
      obtain ⟨j, hj, rfl⟩ := List.mem_iff_getElem.mp hx
      exact hall j hj (by omega)
  · -- body: x := xs[i]; m := max m x; i := i + 1
    simp only [BitVec.slt, decide_eq_true_eq] at hc
    dsimp only
    have hiu : i.ult len = true := ult_of_toInt (by omega) hc
    have hin : (i.toNat : Int) = i.toInt := toNat_of_toInt_nonneg (by omega)
    have hix : i.toNat < xs.length := by omega
    have hload := heapLoad_arr (k := i.toNat) (by decide) hh (by simpa using hix)
    cases n with
    | zero => exact wpPtN_zero prog_f2
    | succ n =>
      apply wpPtN_of_block prog_f2 rfl
      wp_simp
      simp [e0, e2, e3, hsz, dp_indexAddr hiu, hload]
      have hi1' : (i + 1).toInt = i.toInt + 1 := by
        rw [BitVec.toInt_add_of_not_saddOverflow] <;> simp [BitVec.saddOverflow] <;> omega
      -- the invariant after one round, for the new maximum m'
      have next : ∀ m' : BitVec 64, m.toInt ≤ m'.toInt → xs[i.toNat].toInt ≤ m'.toInt →
          ∀ j (hj : j < xs.length), (j : Int) < (i + 1).toInt → xs[j].toInt ≤ m'.toInt := by
        intro m' hm hx j hj hji
        rw [hi1'] at hji
        by_cases hji' : (j : Int) < i.toInt
        · exact Int.le_trans (hall j hj hji') hm
        · have : j = i.toNat := by omega
          subst this; exact hx
      cases hx : m.slt xs[i.toNat]
      · -- no update: block 7 with m
        simp only [BitVec.slt, decide_eq_false_iff_not, Int.not_lt] at hx
        dsimp only
        cases n with
        | zero => exact wpPtN_zero prog_f2
        | succ n =>
          apply wpPtN_of_block prog_f2 rfl
          wp_simp
          simp [hsz, e3]
          apply wpPtN_le (show n ≤ n + 1 + 1 by omega) (ih _ _ _)
          refine ⟨by simp [hsz], by simp [hsz]; exact e0, hh, m, i + 1, by simp [hsz], by simp [hsz],
            by omega, by omega, next m (Int.le_refl _) hx⟩
      · -- update: block 6 loads xs[i] again, block 7 with it
        simp only [BitVec.slt, decide_eq_true_eq] at hx
        dsimp only
        cases n with
        | zero => exact wpPtN_zero prog_f2
        | succ n =>
          apply wpPtN_of_block prog_f2 rfl
          wp_simp
          simp [e0, e3, hsz, dp_indexAddr hiu, hload]
          cases n with
          | zero => exact wpPtN_zero prog_f2
          | succ n =>
            apply wpPtN_of_block prog_f2 rfl
            wp_simp
            simp [hsz, e3]
            apply wpPtN_le (show n ≤ n + 1 + 1 + 1 by omega) (ih _ _ _)
            refine ⟨by simp [hsz], by simp [hsz]; exact e0, hh, xs[i.toNat], i + 1, by simp [hsz],
              by simp [hsz], by omega, by omega, next _ (Int.le_of_lt hx) (Int.le_refl _)⟩

/-- `Max` returns a value no element of the slice exceeds, for every slice of
64-bit integers (its length below 2^63, as any Go slice). -/
theorem max_correct (xs : List (BitVec 64)) (len : BitVec 64) (hlen : len.toInt = xs.length)
    (s : HState Val) (hs : s.heap[1]? = some (.arr (xs.map (.int 64)))) :
    wp ⟨2, 0⟩ (maxPost xs) (bindParams (Array.replicate 22 default) [0] [.slice 1 0 len len]) s := by
  have hL : len.toInt < 2 ^ 63 := by have := @BitVec.toInt_lt 64 len; simpa using this
  apply wpPt_of_block prog_f2 rfl
  intro k
  wp_simp
  cases hc : (0#64).slt len
  · -- empty: m = 0, i = 1
    dsimp only
    cases k with
    | zero => exact wpPtN_zero prog_f2
    | succ k =>
      apply wpPtN_of_block prog_f2 rfl
      wp_simp
      apply wpPtN_of_wpPt
      apply wpPt_loop prog_f2 (maxInv xs len) (max_step xs len hlen hL)
      refine ⟨by simp, by simp, hs, 0, 1, by simp, by simp, by simp, ?_, ?_⟩
      · simp; omega
      · intro j hj hj1
        simp only [BitVec.slt, decide_eq_false_iff_not, Int.not_lt] at hc
        simp at hc; omega
  · -- non-empty: m = xs[0], i = 1
    dsimp only
    simp only [BitVec.slt, decide_eq_true_eq] at hc
    simp at hc
    have hne : 0 < xs.length := by omega
    have h0lt : (0#64).ult len = true := ult_of_toInt (by simp) (by simpa using hc)
    cases k with
    | zero => exact wpPtN_zero prog_f2
    | succ k =>
      apply wpPtN_of_block prog_f2 rfl
      wp_simp
      simp [dp_indexAddr h0lt, heapLoad_arr (k := 0) (by decide) hs (by simpa using hne)]
      cases k with
      | zero => exact wpPtN_zero prog_f2
      | succ k =>
        apply wpPtN_of_block prog_f2 rfl
        wp_simp
        apply wpPtN_of_wpPt
        apply wpPt_loop prog_f2 (maxInv xs len) (max_step xs len hlen hL)
        refine ⟨by simp, by simp, hs, xs[0], 1, by simp, by simp, by simp, ?_, ?_⟩
        · simp; omega
        · intro j hj hj1
          have : j = 0 := by simp at hj1; omega
          subst this; exact Int.le_refl _

#print axioms max2_correct
#print axioms countUp_correct
#print axioms max_correct

end Go2Lean.Proofs.Functional
