---
name: go2lean-spec
description: "Plans and writes go2lean specs for a Go repository: decides which functions to check and with which oracle (documented contract, reference implementation, round trip, idempotence, invariant, determinism, bytes vs runes, termination), writes them as go2lean spec functions from the documentation, runs go2lean, and classifies every verdict. Use when hunting defects in a Go module with go2lean, when choosing what to check in an unfamiliar Go repo, when writing or reviewing go2lean:spec or go2lean:sequence functions, or when a go2lean counterexample, an unknown verdict or a spurious replay needs interpreting. Not for writing Lean proofs (go2lean prove) or for extending the model itself."
---

# go2lean specs

go2lean finds a defect only where a spec says what the code should do. The
search, the model and the replay are mechanical; choosing the oracle is the
work. Most Go defects are wrong results, not panics (docs/eval/go-defects.md),
so a spec that only says "does not panic" misses most of them.

## Workflow

1. **Profile the repository.** Run
   `go run .claude/skills/go2lean-spec/scripts/profile.go <module-dir>`
   from the go2lean checkout. It lists, per package:
   - entry points and the input surface (functions taking `[]byte`, `string`,
     `io.Reader`; HTTP handlers);
   - documented contracts (doc comments that say what a function returns or
     guarantees);
   - function pairs (Encode/Decode, Parse/String, Marshal/Unmarshal,
     Escape/Unescape);
   - signs of a standard (imports, RFC or spec names in the docs);
   - concurrency and `unsafe` use;
   - fix-commit hotspots and existing fuzz targets.
2. **Plan.** Rank targets and pick one oracle per target from
   references/oracle-catalog.md. Prefer, in order:
   - entry points over helpers;
   - documented contracts and references over generic properties;
   - fix-commit hotspots over code that has never been fixed;
   - code nobody fuzzes over code that already has fuzz targets.

   Write the plan down (target, oracle, why, which defect class it could
   catch) before writing any spec.
3. **Write specs blind.** Read the documentation, the signatures and the
   types, not the function bodies. A spec written from the body restates
   the body, bugs included. Read a body only to build an input value the
   docs do not explain. The format and the modelled subset are in
   references/writing-specs.md.
4. **Run.** `go2lean run -workspace <ws> -module <module-dir> -deps <package>`.
   Every spec is searched in the model, and every counterexample is replayed
   as a real Go test. `go2lean fuzz` hands the same specs to `go test -fuzz`;
   run both, since they find different things.
5. **Interpret every verdict** with references/interpreting-verdicts.md:
   - a confirmed counterexample is a code bug, a wrong spec or an ambiguous
     contract;
   - unknown and spurious results are model gaps.

   Record each model gap in the ledger:
   `go run .claude/skills/go2lean-spec/scripts/gaps.go <ws>`. The model is
   extended from the ledger, not in advance.
6. **Report** each finding with:
   - the shrunk input;
   - the documentation it contradicts (quoted);
   - its classification;
   - its tier (the verdict's `rank`).

   Report the cases you are unsure of as such. A suppressed finding cannot
   be triaged by anyone else.

## The two ways a spec checks nothing

- **Tautology.** `ret == strings.ToUpper(s)` for a function that calls
  `strings.ToUpper` agrees with every bug they share. A spec must constrain
  the result without recomputing it. A relation, a reference or the docs
  does that; a second copy of the code does not.
- **Vacuity.** A precondition that returns true for almost every input
  leaves the search nothing to check. Each hunt's summary ends with
  `spec checked N, vacuous M`; when M dwarfs N, move the constraint into
  the input (derive it from a generated field) instead of filtering.

## Strength

From weakest to strongest:
1. no panic;
2. an invariant (range, sign, length);
3. a relation (idempotence, symmetry, monotonicity, permutation
   invariance);
4. a round trip;
5. a documented contract;
6. agreement with a reference.

Write the strongest one the code supports. If only "no panic" applies, say
so: that is a finding about the code's testability, not a spec to pad the
count.
