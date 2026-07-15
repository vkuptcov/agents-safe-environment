# Agent Router

This file is a router, not a knowledge dump. The system of record lives in `docs/`;
review artifacts and tracked technical debt live in `docs/reviews/`.

## Startup Order
1. Read `ARCHITECTURE.md` for runtime topology and module boundaries.
2. Read `docs/index.md` for authoritative documentation routing.
3. Open only the task-relevant docs (for example design docs, exec plans, testing, operations).

## Routing
- Documentation routing: `docs/index.md`.
- Runtime topology, module boundaries, and change-boundary rules: `ARCHITECTURE.md`.
- Documentation conventions: `docs/README.md`.
- Design-doc format rules: `docs/design-docs/README.md`.
- Design-doc catalog and subsystem contracts: `docs/design-docs/index.md`.
- Operations-doc and runbook format rules: `docs/operations/README.md`.
- Operations runbook catalog: `docs/operations/index.md`.
- Test suite selection and completion gate: `docs/testing.md`.
- Credential handling and secret-safety rules: `docs/security.md`.
- Dependency approval and update policy: `docs/dependencies.md`.
- Makefile command catalog: `docs/makefile-reference.md`.
- Execution plans and lifecycle states: `docs/exec-plans/README.md`; plan catalog: `docs/exec-plans/index.md`.
- Review format and implementation-response rules: `docs/reviews/README.md`; review artifact catalog:
  `docs/reviews/index.md`.

## Working Rules
- Communicate as one seasoned engineer to another; prioritize precision and quality over verbosity.
- Keep implementations small, readable, and scoped to what was requested.
- Keep durable knowledge in the targeted docs close to the subsystem it governs.
- Keep `AGENTS.md` routing-only; do not duplicate mutable project state here.
- If runtime behavior, architecture, persistence contracts, or integration behavior changes, update the matching
  document in `docs/design-docs/` in the same change.
- For non-trivial work, create an execution plan in `docs/exec-plans/active/`; move it to
  `docs/exec-plans/review/` after implementation is done, and to `docs/exec-plans/completed/` only after
  owner/review acceptance.
- New plan files in `docs/exec-plans/active/` must use `YYYY-MM-DD-<slug>-exec-plan.md`.
- For review requests, put detailed reports under `docs/reviews/`: active-feature reviews in ignored
  `docs/reviews/feature-review/`, broader project reviews in tracked `docs/reviews/code-review/`.
- When addressing review findings, update the source review report with implementation responses instead of leaving
  notes only in chat.
- Track only long-lived deferred work in `docs/reviews/tech-debt-tracker.md`; feature-specific findings stay in
  `docs/reviews/feature-review/` until resolved or deliberately promoted.
- Ask for explicit approval before adding any dependency.
- Never expose or commit real credentials or tokens.
- After any code change, run the smallest required suite from `docs/testing.md`.
- Format changed Go files with `gofmt` before validation.
- After documentation changes, run `make check-docs`.
- Do not consider work complete until required checks pass or an environment blocker is reported explicitly.

