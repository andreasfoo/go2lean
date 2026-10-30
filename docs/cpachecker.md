# What go2lean can take from CPAchecker

Researched 2026-10-30, against go2lean at `848d145`. Sources are linked inline. The study led to issues #84 (havoc relevance), #85 (refinement loop), #86 (`no_hang`) and #87 (setup snapshots); the roadmap is #53.

**Short answer.** Running CPAchecker on go2lean's models is not worth doing. Several of its ideas are worth implementing over go2lean's own IR, and the best of them need no SMT solver. Each re-uses go2lean's interpreter, its havoc log or its replay verdicts. The ideas that fit:

- CEGAR, with the precision taken to be "how much of the code is modelled" and the replay stage acting as the feasibility check.
- Explicit-value interpolation done by re-execution.
- Summaries in the style of BAM (block-abstraction memoization).
- Termination checking recast as safety, by detecting revisited states.

Predicate abstraction, IMPACT and SMT interpolation over go2lean's whole heap model fit badly. CPAchecker's concurrency analyses are not where go2lean should look for help.

## 1. CPAchecker in brief

- **CPA / CPA+.** An analysis is a "configurable program analysis": an abstract domain, a transfer relation, merge and stop operators, and a precision that can be adjusted while the analysis runs. Analyses compose as products, and one reachability algorithm runs them all ([Configurable Software Verification, CAV'07; Dynamic Precision Adjustment, ASE'08](https://cpachecker.sosy-lab.org/)).
- **CEGAR + predicate abstraction, lazy abstraction, IMPACT.** These are SMT-based. Spurious abstract paths are refined with interpolants. BMC, k-induction, predicate abstraction and IMPACT are implemented in one framework and compared there ([Beyer, Dangl, Wendler, JAR 2018](https://www.sosy-lab.org/research/pub/2018-JAR.A_Unifying_View_on_SMT-Based_Software_Verification.pdf)).
- **Value analysis with CEGAR.** The analysis tracks explicit values only for the variables in its precision. Refinement adds variables, using *interpolation over constraint sequences, without an SMT solver* ([Beyer & Löwe, FASE'13](https://www.sosy-lab.org/research/pub/2013-FASE.Explicit-State_Software_Model_Checking_Based_on_CEGAR_and_Interpolation.pdf); [ETAPS interview](https://etaps.org/blog/011-dirk-beyer/)).
- **Symbolic execution with CEGAR.** CEGAR is applied to two domains: the values and the path constraints ([Beyer & Lemberger, ISoLA'16](https://www.sosy-lab.org/research/pub/2016-ISoLA.Symbolic_Execution_with_CEGAR.pdf)).
- **k-induction with continuously refined invariants.** A data-flow invariant generator runs in parallel and feeds the inductive step ([CAV'15](https://www.sosy-lab.org/research/cpa-k-induction/)).
- **BAM.** Blocks such as function bodies are summarised, and the summaries are cached and reused per input abstraction ([Wonisch & Wehrheim ICFEM'12; Beyer & Friedberger FSE'22 slides](https://www.sosy-lab.org/research/prs/2022-11-15_FSE2022_BAM-Interprocedural_Dirk.pdf)).
- **Termination.** There are two routes: termination-to-safety (checking for revisited states) and LassoRanker on lassos ([CPAchecker 3.0 tutorial, arXiv 2409.02094](https://arxiv.org/pdf/2409.02094)).
- **Concurrency.**
  - ThreadingCPA explicitly enumerates the interleavings of a bounded number of threads ([arXiv 1612.04983](https://arxiv.org/pdf/1612.04983)).
  - PredicateCPA cannot be combined with ThreadingCPA ([issue #464](https://gitlab.com/sosy-lab/software/cpachecker/-/issues/464)).
  - Data races are found thread-modularly by CPALockator, which reported 52% false alarms on Linux drivers ([ECEASST](https://eceasst.org/index.php/eceasst/article/view/2246)).
- **Witnesses.** Violation and correctness witnesses use the YAML format 2.0 ([SPIN'24](https://www.sosy-lab.org/research/pub/2024-SPIN.Software_Verification_Witnesses_2.0.pdf)). Violation witnesses can be validated by executing them as tests ([Tests from Witnesses, TAP'18](https://www.sosy-lab.org/research/pub/2018-TAP.Tests_from_Witnesses_Execution-Based_Validation_of_Verification_Results.pdf)). Analyses can share information to generate tests ([CoVeriTest](https://www.sosy-lab.org/research/coop-testgen/)). Verification can be restricted to the paths a change modified ([Difference Verification with Conditions, SEFM'20](https://www.sosy-lab.org/research/difference/)).

**Unknown code.** CPAchecker does not solve model incompleteness either. A function without a body returns a nondeterministic value, configured through `nondetFunctions` and the "safe unknown functions" options ([ISPRAS #1018](https://forge.ispras.ru/issues/1018)). The library environment is modelled *outside* the verifier: for the Linux kernel, the LDV/Klever tools generate environment models ([Klever profiles](https://klever.readthedocs.io/en/stable/_sources/dev_verifier_profiles.rst.txt)). go2lean's havoc is the same choice. What CPAchecker adds is a disciplined way to *refine* such a choice.

## 2. Running CPAchecker itself (secondary)

- **(a) Export to C or LLVM.** CPAchecker 4.1 disabled its LLVM frontend because it was old and unmaintained ([4.1 release notes, Zenodo](https://zenodo.org/records/11074229/latest)), so C is the only input.
  - Lowering go2lean's IR to C keeps havoc as `__VERIFIER_nondet_*`. It loses the exact Go semantics unless all of it is re-encoded in C: strings, slices with cap, maps, interfaces, panics, and goroutines and channels mapped onto pthreads.
  - Turning the witness back into a go2lean counterexample means mapping C lines back to IR blocks.
  - CPAchecker placed 10th of 27 in ConcurrencySafety at SV-COMP 2025 (1770 points against Deagle's 4604) and 12th of 19 in NoDataRace ([SV-COMP 2025 results](https://sv-comp.sosy-lab.org/2025/results/results-verified/)).
- **(b) Another path for Go.** I found no Go-to-C or Go-to-Java path into CPAchecker. The Go tools that exist target SPIN (Gomela) or Viper (Gobra).
- **Verdict.** It is weeks of work, adds a second semantics to keep in sync, gives the weakest part of CPAchecker for the concurrency cases, and none of go2lean's replay or havoc origins would survive the round trip. If a solver is wanted, a direct SMT-LIB export (design.md §5.3, rung 5) is cheaper and keeps go2lean's semantics.

## 3. Bottleneck 1: model incompleteness (havoc)

go2lean already has three of the four pieces of a CEGAR loop:

- an abstract model: havoc at every unmodelled site, with `Prog.siteOrigin` and `ABIHavoc{Site, Reason}` saying where each site comes from;
- an abstract counterexample, carrying the havoc log `HState.log` of (site, iteration, value);
- a feasibility check, which is the replay. Its verdicts are `oracle_holds_in_real_run`, `no_panic_in_real_run`, and `holds_on_model_schedule` (the schedule was followed and the oracle still held). `Diagnosis.HavocUsed` names the sites.

The missing piece is **refinement**. Today a spurious verdict is printed and the run stops (`internal/pipeline/pipeline.go`, `printVerdict`). Nothing feeds back, and `ModelGap` exists in the contract but nothing emits it.

### 3.1 Havoc relevance by re-execution (Beyer–Löwe interpolation without SMT)

- **Idea.** In CPAchecker's value analysis, an "interpolant" for an infeasible path is the smallest set of variables whose values are needed to keep the path's conclusion. The analysis finds it by re-checking the path with variables dropped.
- **In go2lean.** For a model counterexample, rerun the same input once per havoc read, changing that read's value and keeping the others. The reads whose change makes the violation go away form the **relevant set**. The rest are noise.
- **Where it plugs in.** `Search.check` already does a coarse version for spec verdicts: the `alts` havoc assignments decide whether havoc "decided" the verdict. Generalise that to a per-read relevance check for every property kind, `no_panic` and the concurrent machine included.
- **What it gives:**
  1. An empty relevant set means the violation does not depend on unknown code, so the replay is very likely to confirm it. This can drive ranking and the order of replays.
  2. A non-empty set names the exact site to refine (3.2), instead of every site the run read. The k009, k038, k043, k045 and k050 cases are all spurious through havoc from sockets, `reflect` or `os/exec`.
  3. The exhaustive engine could enumerate only the relevant sites. That turns some `exhausted_havoc` results into certificates over those sites.
- **First experiment.** Add `relevantHavoc` to `lean/Go2Lean/Search.lean`, called on each counterexample and emitted in its JSON. Then compare its size with `Diagnosis.HavocUsed` on the spurious cases in `docs/eval/conc-recent/ledger.txt` and in `examples/`.
- **Cost.** About a day. It is pure Lean re-execution with no solver. One cost: k-fold reruns of a run with heavy setup, which 3.4 addresses.

### 3.2 CEGAR over model precision: replay-driven, lazy de-havocing

- **The loop.** Treat the precision as a per-site setting with four levels: **free** (havoc), **summary** (3.3), **translated** (compile that callee with its dependencies, for that callee only), or **stub** (a hand-written or agent-written model of a library function). Start at free. When a counterexample is spurious, raise the precision of its relevant sites (3.1), rebuild, and search again. When it is confirmed, report it. Stop when nothing is left to refine or the budget runs out.
- **What makes it lazy.** Only the sites on spurious paths are refined. That matters because translating everything (`-deps`) is what runs out of memory on nats-server (k023).
- **Progress.** Do not count on CPAchecker's guarantee (an interpolant removes the path). Check it directly: after refinement, revalidate the old counterexample in the model under principle P4. It must no longer violate the property. If it still does, the refinement made no progress, so mark the site "unrefinable" (an `assume` note) and report the result as `inconclusive`.
- **First experiment.** Write a script around existing commands; no new code yet. For each spurious case in conc-recent and `docs/eval/search-vs-fuzz/toy-gaps.md` #2 (`io.ReadAll` on a `strings.Reader`):
  1. take the callee from `guessedCalls`;
  2. recompile with that dependency translated;
  3. rerun `hunt` and `poc`;
  4. count spurious → confirmed, spurious → holds, and no change.
- **Where the code goes.** `internal/pipeline` (the loop), `internal/poc/run.go` (emit `ModelGap` records), and the frontend's per-callee `-deps` selection.
- **Lean side.** Nothing hard. The limit is frontend coverage: sockets, `reflect` and `os/exec` cannot be translated, so those sites need 3.3.

### 3.3 Summaries for unmodelled calls (BAM-style, learned from real runs)

- **Idea.** BAM caches a summary per block and reuses it. go2lean cannot compute a summary of a socket read, but its replay *runs* the real code.
- **In go2lean.** Make the replay overlay wrap each unmodelled call named by `guessedCalls`, and log its arguments and results. Feed the log back as a table: havoc site to a list of observed values, keyed by argument where the arguments are modelled. The model then draws from observed behaviour instead of from arbitrary values.
- **Soundness.** This is an under-approximation. It must go through the existing `assume` mechanism, so that results are `exhausted_assuming`, never certificates. For finding bugs that is the right trade, because the replay remains the judge.
- **Klever's precedent.** Klever and LDV write environment models by hand. The analog here is the "stub" level in 3.2, and the optional agent could draft those stubs in Go.
- **First experiment.** Wrap one callee in the k043 or k045 overlay, record ten real results, and inject them as the havoc values for that site. Does the model still produce the spurious counterexample?
- **Where it touches.** `internal/poc` (overlay wrappers), the havoc oracle in `Search.lean` (`havocOf`), and `Core.lean`'s `HState.assume`.

### 3.4 Memoising the setup prefix (the k016 cost)

- **The problem.** Every trial and every schedule reruns from the start (`Search.exhaust` replays the forced schedule prefix from scratch). For k016, one trial of a 16384-stripe setup takes minutes.
- **The fix.** Cache the state at the first draw that depends on the input, the schedule or havoc, and resume from it. Lean values are persistent, so a snapshot of the `HState` and the goroutine frames is free to *keep*.
  - The catch: the first write after a resume copies the heap `Array`, because a shared array is no longer updated in place. That costs O(heap) per trial, against minutes today.
  - Stateful DFS over schedules can resume from the state at the last choice point instead of from the root. This is CPAchecker's ARG reuse in concrete form.
- **Data structure.** Go arrays in the heap are `.arr (List Val)`, and `setPath` uses `List.set`, which is O(n). For large arrays, use `Array Val` at run time. The WP proofs rely on `List` lemmas (`heapStore_arr`, `List.set_set_perm`), so keep `List` in the proof-facing definitions and connect the two with a `@[csimp]`-proved equivalence, or switch representation in the search-only build.
- **First experiment.** Time k016's setup against its post-setup run. Then snapshot after setup for test targets that take no inputs.
- **Where it touches.** `Search.lean` (`run`, `exhaust`), `Dialect/GoConc.lean` (`advance`, `stepGor`), and `Dialect/GoSSA.lean` (`setPath`).

### 3.5 Path-sensitive input search: from cheap to symbolic

The "two equal string args" problem is what symbolic execution solves. go2lean's interpreter is concrete over `BitVec` and byte lists.

- **A symbolic dialect.** `Dialect` takes `Val` as an outParam, so a symbolic GoSSA instance is possible. But it would be a second 3200-line dialect.
- **Cheap step first (not from CPAchecker).** Log comparisons, in the style of AFL++'s cmplog and Redqueen: record the operands of `eq`/`lt` at `br` terminators whose outcome went the uncovered way, and add them to the value pool of the next trials. Add branch-coverage feedback to `Search.run` as well, since it has none. The Go fuzzer has both, which is part of why it found 22 of the 25 bugs.
- **Concolic second.** Shadow only integer and string values that come from the inputs. Negate one branch at a time.
- **Solver.** Two options:
  - Lean's own **executable** bit-blaster (`Std.Tactic.BVDecide.Bitblast`) plus the **CaDiCaL binary shipped with the toolchain**, called through `Lean.Meta.Tactic.BVDecide.External.satQuery`. Both are present in v4.35.0's `bin/` and `src/`. This covers bit-vectors only.
  - For strings, an SMT solver as a subprocess. The replay stage is the certificate, so the solver's answer needs no proof.

## 4. Bottleneck 2: spec scarcity

### 4.1 Termination as safety: hang detection by revisited states

- **Idea.** CPAchecker's termination-to-safety reports non-termination when a state repeats ([tutorial](https://arxiv.org/pdf/2409.02094); [arXiv 2608.07397](https://arxiv.org/pdf/2608.07397) notes that the strong tools find non-termination this way).
- **In go2lean.** A deterministic interpreter that revisits the same state is in a definite infinite loop for that input. "Same state" means the same (function, block, environment, heap) at a loop header, with no havoc or schedule draw in between. Today such runs end as `outOfFuel` and are discarded (k010, k016, k038, k044 all hit fuel).
- **The property.** A new property `no_hang`, written by no one:
  - check states at back edges at exponentially spaced steps (Brent's cycle detection) to amortise the hashing cost;
  - in `GoConc`, a repeated global state under a fixed cyclic schedule is a livelock or spin candidate;
  - replay with the existing watchdog.
- **First experiment.** When a run returns `outOfFuel`, rerun it with state hashing at back edges. Count lassos on `examples/` and on the conc-recent fuel cases.
- **Cost.** Needs `Hashable Val` and lives in `Core.lean`'s `runBlock` or in a wrapper in `Search.check`.
- **Done (step 1).** Out-of-fuel sequential runs are rerun in `Go2Lean/Hang.lean`, a copy of the interpreter with Brent's detection at back edges (equality, not hashing). The hunt summary counts the runs that loop forever ([semantics](semantics.md#3-failure)). The fuel cases of conc-recent are all concurrent, which this step does not cover. - **Done (step 2).** The `no_hang` property, for every function with a loop. Its counterexamples are replayed under the 2s watchdog, and both bugs in `examples/hang` are confirmed. Open: the concurrent case (livelock).

### 4.2 Relational specs nobody writes

go2lean has the `equivalence` property kind but generates none. The no-spec sources:

- **Version differencing.** Check the same function at revision n−1 and revision n, restricted to the paths the change touched. This is CPAchecker's difference verification. A disagreement is a behaviour change, not necessarily a bug, so it is reported for review.
- **Natural pairs found by name and signature.**
  - `Marshal`/`Unmarshal` and `String`/`Parse` should round-trip.
  - Streaming and non-streaming converters should agree (history.md shows these found real bugs when written by hand).
  - `Normalize`-style functions should be idempotent.
  - A `Clone` should be equal to its source.

  These follow the explicit opt-in pattern of `-compare-order` (docs/builtins.md).
- **Where it touches.** `internal/builtin` and `internal/agent` prompts.
- **First experiment.** Generate round-trip properties for every `(Encode|Marshal|Format)X`/`(Decode|Unmarshal|Parse)X` pair in the eval repos and count confirmed findings.

### 4.3 Automatic instrumentation beyond panics

- **SV-COMP's properties.** These are unreach-call, memory safety, no-overflow, termination and no-data-race. go2lean already covers the Go analogues of memory safety, races, deadlocks and leaks.
- **Overflow.** Signed overflow wraps in Go and is not a bug, so a C-style no-overflow property would flood users with false reports. A narrower version is useful: an overflowed (wrapped) value that flows into an index, a `make` length, a slice bound or a loop guard. Implement it as a taint bit in the GoSSA dialect and report the results at a low tier. limitations.md §3 already says confirmed ≠ actionable.
- **Value.** Medium.

### 4.4 Invariants, k-induction and witnesses

- **Loop invariants for the WP proofs.** k-induction with continuously refined invariants maps onto `Proof/WP.lean`:
  - generalise the cut-point rule `wpPt_loop` to k unrollings;
  - feed it candidates mined from model traces (the Houdini filter already planned in related-work.md).
- **Limits for bug-finding.** An invariant mined from runs holds on those runs. It finds a bug only when a different input (from the exhaustive engine) breaks it, which is Daikon-style anomaly reporting, and that is noisy. Its value is for proofs (pain point 4), not for finding bugs.
- **Witnesses.** go2lean's PoC bundle already *is* a violation witness validated by execution, which is the TAP'18 idea. Its `exhausted`/`proved` records with invariants are correctness witnesses checked by Lean's kernel. Exporting SV-COMP YAML is pointless: the format is tied to C source locations and no Go validator exists.

## 5. What Lean 4 makes hard

| Need | Available | Assessment |
|---|---|---|
| SAT / bit-vectors | Std bit-blaster (executable) + CaDiCaL in the toolchain + `satQuery`; LRAT checking for proofs | Usable now for integer path constraints |
| SMT (strings, arrays) | lean-cvc5 FFI: "minimal interface", needs clang/libc++ 19, no `getStringValue` ([repo](https://github.com/abdoo8080/lean-cvc5)); lean-smt is a proof tactic, with bit-vectors listed as future work ([arXiv 2505.15796](https://arxiv.org/pdf/2505.15796)) | Fragile. For bug-finding, prefer an SMT-LIB subprocess from the Go orchestrator |
| Interpolation | No interpolating solver from Lean; cvc5 has `get-interpolant` but its exposure through the FFI is unconfirmed | Use re-execution interpolation (3.1), which needs no solver |
| Performance | `.arr (List Val)` updates are O(n); a full rerun per schedule; race shadow in HashMaps | 3.4. Snapshots conflict with in-place `Array` updates (copy on first write) |
| Proof coupling | Proofs rely on `List` lemmas and `simp` unfolding of the interpreter | Keep the proof semantics; optimise behind `@[csimp]` or a search-only build |

## 6. Ranked recommendations (value against the two bottlenecks, per cost)

| # | What | Bottleneck | Cost | First experiment | Touches |
|---|---|---|---|---|---|
| 1 | Havoc relevance by re-execution (3.1) | incompleteness | S | `relevantHavoc` on each counterexample; compare with `HavocUsed` on conc-recent's spurious cases | `Search.lean` (`check`), counterexample JSON, `poc` diagnosis |
| 2 | Replay-driven precision refinement loop (3.2) | incompleteness | M | Script: spurious case → translate only `guessedCalls` → rerun; count verdicts that flip | `internal/pipeline`, `internal/poc` (`ModelGap`), frontend `-deps` |
| 3 | Hang detection by revisited states (4.1) | specs | S–M | Rehash `outOfFuel` runs at back edges on `examples/` and on the fuel cases | `SSA/Core.lean`, `GoConc.lean`, `poc` watchdog |
| 4 | Setup-prefix snapshot + `Array` for large heap arrays (3.4) | incompleteness (fuel and time) | M | Profile k016; snapshot after setup for input-free tests | `Search.lean`, `GoConc.lean`, `GoSSA.lean` |
| 5 | Comparison logging + coverage feedback, then concolic search with Std bit-blaster + CaDiCaL (3.5) | search reach | S, then L | Comparison logging on the equal-strings case | `Search.lean` (`run`, `genArgs`) |
| 6 | Observed summaries for unmodelled calls (3.3) | incompleteness | M–L | Wrap one k043/k045 callee in the overlay and inject the recorded results | `internal/poc` overlay, `havocOf`, `assume` |
| 7 | Generated relational specs (4.2) | specs | M | Round-trip pairs across the eval repos | `internal/builtin`, agent prompts |
| 8 | k-induction rule + trace-mined invariants (4.4) | proofs | M | `wpPt_loop` with k=2 on the sort proof | `Proof/WP.lean` |
| 9 | Overflow-into-index taint (4.3) | specs | M | Taint bit on `add`/`mul` results reaching `indexAddr`/`makeSlice` | `GoSSA.lean`, `Search.lean` |

**Not recommended:**

- running CPAchecker through C export;
- SV-COMP witness export;
- predicate abstraction, IMPACT or SMT interpolation over the full Go heap model in Lean;
- CPAchecker's concurrency analyses as a model. For schedules, look at DPOR and at the SV-COMP concurrency leaders (Deagle, Dartagnan).

## 7. License and SV-COMP

- **License.** CPAchecker is Apache 2.0 ([GitHub README](https://github.com/sosy-lab/cpachecker)), which is compatible with borrowing ideas, or code with attribution, into MIT-licensed go2lean. MathSAT, which ships with it, is licensed for research and evaluation only.
- **SV-COMP 2025** ([results](https://sv-comp.sosy-lab.org/2025/results/results-verified/)):
  - CPAchecker won gold in MemSafety (4892 points);
  - it placed 2nd Overall, behind UAutomizer;
  - it placed 10th of 27 in ConcurrencySafety (1770 points against Deagle's 4604);
  - it placed 12th of 19 in NoDataRace-Main.
- **Witness validation.** Its 4.0 validator placed in the top three in almost every category ([TACAS'25](https://sosy-lab.org/research/pub/2025-TACAS.CPAchecker_4.0_as_Witness_Validator.pdf)).
