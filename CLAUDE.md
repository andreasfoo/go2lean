# go2lean: working rules

go2lean turns Go packages into a Lean model, searches it for bugs, and
replays each candidate as a real `go test`. Keep the name go2lean.

## Workflow

- Work on a feature branch and open a pull request into `main`; never push
  to `main` directly. Track planned work in GitHub issues (concurrency:
  andreasfoo/go2lean#2, functional proofs: andreasfoo/go2lean#1).
- Once CI is green and the PR mergeable, merge it and carry on with the
  next item; no need to wait for a go-ahead between PRs.
- Every PR updates the docs it affects, the README above all. Keep the
  README short (what it is, quick start, how it works, links); details go
  to the docs it links: `docs/usage.md`, `docs/pipeline.md`,
  `docs/validation.md` (ground-truth table, evaluations),
  `docs/reference.md` (supported Go, roadmap, counts), `docs/concurrency.md`.
- Before pushing: `go vet ./...`, `go test ./internal/...`,
  `scripts/regress.sh <ws>` (update `testdata/regress/examples.golden`
  only for new or intended outcome changes), `scripts/proofs.sh <ws>`, and
  the `json-diff.sh` checks CI runs.
- Do not post to other projects' trackers (issues, PRs, comments) without
  the user's consent.

## Words

- The pipeline has three stages: the **compile stage** (Go to the Lean
  model), the **spec & search stage** (properties and the engines), the
  **replay stage** (counterexamples replayed as `go test`, with a PoC).
  Do not call them M1, M2, M3 in docs, output or PRs.
- Code, docs, commits and PRs are in English.

## Layout

- `internal/frontend/gossa` (compile stage), `internal/constraint`
  (spec & search stage), `internal/poc` and `internal/replay` (replay
  stage), `lean/Go2Lean` (the model's semantics and the engines).
- Design notes: `docs/design.md`, `docs/semantics.md`,
  `docs/concurrency.md`, `docs/limitations.md`, `docs/related-work.md`.
- Ground truth: `examples/`; GoKer evaluation: `scripts/goker.sh`.
