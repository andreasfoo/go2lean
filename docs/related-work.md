# Related work: translating programs into Lean, and what go2lean reuses

Surveyed 2026-10-23 (academic and open source), to decide what go2lean
builds itself and what it takes from others. Status notes come from the
projects' own pages and papers; check them before depending on a detail.

## Translating a programming language into Lean

| Source | Project | Style | State |
|---|---|---|---|
| Rust | Aeneas + Charon (Ho, Protzenko) | shallow: safe Rust (MIR, via Charon's LLBC) to pure Lean functions, using ownership to remove the heap | the most used Rust-to-Lean path; no unsafe Rust; Lean and HOL4 the mature backends |
| Rust | hax (Cryspen) | `cargo hax into lean` now goes through Charon and Aeneas; the Aeneas engine is being merged into hax; proof attributes choose `grind` or `bv_decide` | active (monthly reports into 2026) |
| Rust | the zkEVM experience report (arXiv 2605.30106) | Aeneas or hax, ArkLib/CompPoly specs, AI provers (Aristotle, Aleph), every proof kernel-checked | the gaps it lists are toolchain drift, extraction limits, missing lemmas |
| LLVM IR, MLIR | Lean-MLIR (opencompl) | deep embedding of SSA with dialects; peephole rewrites proved | integers and multi-block; memory, floats, richer loops future work |
| LLVM IR | Trivet (arXiv 2609.19583) | translation validation: deterministic proof scaffolds in Lean, LLM for the rest | 147 of 148 transformations decided |
| Armv8 machine code | LNSym (AWS) | ISA semantics and a symbolic simulator in Lean | used for crypto code; Sail now has a Lean backend too |
| Python | FVSpec (arXiv 2606.01008) | LLM agents turn Python property-based tests into Lean specifications (`sorry` bodies) | a benchmark, not a translator of code |
| Python | PastaLean / PyAstLean | AST-based transpiler, no LLM | talk slides (2026), no paper found |
| several | Strata (AWS) | a family of intermediate verification languages (Laurel) implemented in Lean, with semantics and proved transformations, front-ends to back-ends | under active development, breaking changes |
| C, C++, Go, Python | LLM "translator" skills, leanlift | an LLM proposes the Lean, a checker validates | no formal relation to the source |
| Go | gnark circuit extractor (reilabs) | Go ZK circuits (gnark) to Lean | circuits only |

**Go has no general translator into Lean.** The Go verification tools
target other provers:

- Goose and Perennial (MIT PDOS): a Go subset translated into GooseLang in
  Coq/Rocq, verified with Perennial (Iris with crash safety); a 2025 MIT
  thesis added channels (a channel model in GooseLang, separation-logic
  specifications of the operations).
- Gobra (ETH): annotated Go to Viper, discharged by SMT; memory safety,
  data-race freedom, user specifications.
- For concurrency bugs without proofs: `go test -race`, goleak, GCatch,
  Gomela (Go to Promela, checked by SPIN).

go2lean's Go frontend (go/ssa to a deep embedding with a Lean interpreter)
fills that gap; it is closest in style to Goose (a deep embedding with a
heap), not to Aeneas (Go has no ownership discipline to make a shallow
translation sound).

## Proof infrastructure in Lean

- **`vcgen` and `Std.WP`** (Lean core; `vcgen` succeeds `mvcgen` in 4.35,
  the version go2lean uses, still marked experimental). A verification
  condition generator for Hoare triples over any program type with a
  weakest-precondition interpretation: `do` programs in any monad, and
  *deeply embedded languages once their weakest precondition is defined
  from an operational semantics* (the release notes name x64 assembler and
  WebAssembly). Loop invariants as clauses; `@[spec]` lemmas per callee;
  verification conditions discharged with `grind`. Linear in program size
  (0.14 ms per loop iteration in the release benchmark).
- **`grind`**: SMT-style automation in core (congruence closure,
  E-matching, linear integer arithmetic, case splits), producing kernel
  proofs; since 4.35 it reaches `BitVec` goals by translating them to
  `Nat` and `Int`. `bv_decide` stays the tool for bit-level goals (its
  certificate check trusts the compiler).
- **Loom and Velvet** (VERSE Lab, NUS): a framework that generates
  foundational verifiers (weakest preconditions in Lean, soundness a Lean
  theorem), and a Dafny-style verifier built with it; `grind` first, then
  an SMT solver (cvc5).
- **Veil** (VERSE Lab, CAV 2025): transition systems and distributed
  protocols in Lean, SMT-discharged invariants, concrete and symbolic
  model checking; an explicit-state checker in the 2.0 preview. Liveness
  is future work.
- **Iris-Lean**: a Lean port of Iris (concurrent separation logic),
  picking up speed in 2026; atomic updates and the total weakest
  precondition still missing.
- **CSLib**: a community library for computer science in Lean (shared
  semantics abstractions such as labelled transition systems; concurrency
  on its later roadmap).
- **AI provers and agents**: Aristotle (Harmonic), Aleph (Logical
  Intelligence), and general code agents under a kernel-checking harness
  (arXiv 2607.06341: all 4,257 lemmas of Iris's core modules in Coq, and
  lemmas of iris-lean); proofs are trusted because the kernel checks them.

## What this changes for go2lean

1. **Keep**: the Go frontend, the deep embedding and its interpreter, the
   search engines, the mutation check and the replay stage. Nothing else turns Go into
   Lean, and nothing else hunts bugs in a model with real-run
   confirmation.
2. **Functional proofs (the sort example, task 55): do not write a program
   logic.** Give go2lean's IR a weakest-precondition interpretation from
   the interpreter (the route `vcgen` documents for deep embeddings), then
   prove with `vcgen`, loop invariants and `grind`. Invariants and the
   remaining obligations are an agent's job, under the kernel. Loom is the
   fallback if `Std.WP` does not fit.
   *Result of the spike (issue #1):* `Std.WP`'s `WP` class takes the
   interpreter's semantics without trouble, but `vcgen` advances through a
   program by its structured constructors (`seq`, `ite`, `while`), and
   go/ssa's control-flow graph has none: with one spec per block, `vcgen`
   applies it once and hands back the rest as a single condition. What
   worked is smaller: a weakest precondition over blocks, an unfolding
   lemma per block, and a cut-point rule for loop headers proved once by
   induction on fuel (`lean/Go2Lean/Proof/WP.lean`), with the existing
   `simp` lemmas and `grind`/`omega` for the conditions. `vcgen` would pay
   off only with a structured form of the program (the frontend could emit
   one: Go source is structured).
3. **The existing prover** (`no_panic` by `simp` and `bv_decide`): move the
   closing step to `grind` where it applies, keeping `bv_decide` for
   bit-level goals; a `grind` proof needs only the kernel.
4. **Concurrency (C1)**: no Lean project models Go's goroutines and
   channels, so the machine in `docs/concurrency.md` stays ours. Take
   Goose's channel model and its specifications as the reference to review
   ours against; take Veil's explicit-state checking as the model for
   state deduplication. Unbounded concurrent proofs would build on
   Iris-Lean, not on a logic of our own; not before it has atomic updates.
5. **Watch**: Strata (a Go front-end into its intermediate languages might
   give verification back-ends for free; its focus is verification with
   annotations, not search), and the Aeneas/hax line (shallow embeddings
   prove more easily, but need the ownership Go lacks).

## Sources

- Aeneas: https://github.com/sonmarcho/aeneas
- hax: https://github.com/cryspen/hax, https://hax.cryspen.com/manual/lean/,
  https://hax.cryspen.com/blog/2026/03/03/this-month-in-hax-february-2026/
- Rust-to-Lean with AI provers: https://arxiv.org/abs/2605.30106
- Lean-MLIR: https://github.com/opencompl/lean-mlir
- Trivet: https://arxiv.org/abs/2609.19583
- LNSym: https://reservoir.lean-lang.org/@leanprover/lnsym; Sail: https://ocaml.org/p/sail/0.20.2
- FVSpec: https://arxiv.org/abs/2606.01008; PastaLean slides: https://east.emergence.ai/images/presentation/pastaLean.pdf
- Strata: https://github.com/strata-org/Strata
- Goose/Perennial: https://pdos.csail.mit.edu/projects/perennial.html; channels thesis: https://dspace.mit.edu/handle/1721.1/159079
- Gobra: https://arxiv.org/pdf/2105.13840
- gnark extractor: https://github.com/reilabs/lean-circuit-compiler
- vcgen: https://lean-lang.org/doc/reference/4.35.0/The--vcgen--tactic/, release notes https://lean-lang.org/doc/reference/4.35.0/releases/v4.35.0/
- grind: https://lean-lang.org/doc/reference/latest//The--grind--tactic/
- Loom, Velvet: https://reservoir.lean-lang.org/@verse-lab/Loom, https://reservoir.lean-lang.org/@verse-lab/Velvet
- Veil: https://reservoir.lean-lang.org/@verse-lab/veil, https://lean-lang.org/use-cases/veil/
- Iris-Lean: https://arxiv.org/pdf/2609.24252, https://iris-project.org/workshop-2026/slides/liu.pdf
- CSLib: https://arxiv.org/html/2602.04846v1
- Code agents for verification: https://arxiv.org/abs/2607.06341
