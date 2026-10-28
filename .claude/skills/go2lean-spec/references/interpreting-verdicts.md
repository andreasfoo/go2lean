# Interpreting go2lean verdicts

A spec's search ends in one of a few states, each with a different next
step.

| state | where it is recorded | what it means | next step |
|---|---|---|---|
| **confirmed** | `verdicts/<cx>.json`, status `confirmed` | real Go reproduced the counterexample | classify it (below) |
| **spurious** | status `spurious` | real Go did not reproduce it: the model and Go differ (`diagnosis` says how) | a model gap or model bug: record it |
| **inconclusive** | status `inconclusive` | the replay could not decide (the input could not be built, or the run did not finish) | read `diagnosis.reason`; often a harness limit, sometimes a model gap |
| **exhausted** | `proofs/<prop>.json`, status `exhausted` | no counterexample on the enumerated domain (`domain`) | evidence, not proof; weigh it against the domain's size |
| **exhausted_havoc** / **exhausted_assuming** | same file | as exhausted, but runs read values the model does not know, or took a recorded under-approximation (`assumptions`) | the search was weaker than it looks: record the assumptions |
| **no verdict, model issues** | `models/gossa/<pkg>/report.json`, `issues` of the function and its callees | the model cannot follow the function | record the gap |

## Classifying a confirmed counterexample

Check the input against what the code promises, in this order of
authority:
1. an external standard (an RFC, a format definition);
2. the types;
3. the doc comment;
4. the package's own tests and examples;
5. the function's name.

| finding | cause | action |
|---|---|---|
| violates a documented guarantee | **code bug** | report it with the input, the doc line and the verdict's tier |
| the input breaks a documented precondition | spec too broad | tighten the spec's precondition |
| the spec contradicts the docs | **wrong spec** | fix the spec |
| an edge the docs never decide | **ambiguous contract** | report it as a question for the maintainers, not as a bug |
| differs from a sibling function or the reference | possible inconsistency | report it, with the uncertainty stated |
| only on an input no caller can build (tier 4) | artifact of the test | note it; do not report it as a bug |

Recurring patterns:
- **A nil receiver or zero value the API never hands out.** Check the
  constructors first.
- **Negative lengths and counts.** These are bugs only when the docs allow
  them.
- **Multibyte text.** This is a real bug whenever the function documents
  "string" without restricting it to ASCII.

## The model-gap ledger

Every spurious, inconclusive, assuming, havoc or model-issue result names
something the model lacks: a package-level variable, an `unsafe`
conversion, an unmodelled standard-library function, a construct.
`go run .claude/skills/go2lean-spec/scripts/gaps.go <workspace>` collects
them into one table:
- **specs:** the written specs the gap affected;
- **properties:** all properties, template `no_panic` included;
- **kind:**
  - `havoc`: an unmodelled call or load a run read;
  - `assumption`: an under-approximation;
  - `model issue`: the report on the target function;
  - `stuck`: the model could not continue;
  - `harness`: an inconclusive replay;
  - `model bug`: a spurious verdict with neither havoc nor an assumption,
    where the model and Go disagree on modelled code.

Rows are sorted by specs first: a gap that blocks written specs matters
more than one that only blocks templates. On the semver evaluation, for
example, the top row is the regexp calls behind six of its specs.

The model (go2lean's stage 1) is extended from this ledger, ordered by how
many valuable checks each gap blocks, and not in advance. A `model bug`
row is fixed first, whatever its count.
