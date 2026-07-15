# Execution Plans

Execution plans track non-trivial implementation work from proposal to completion and make deferred
scope explicit. They describe the path to implementation, not the durable design contract — link the
owning design doc when one exists; do not duplicate it here.

## Locations
- `active/`: implementation is not started or is in progress.
- `review/`: implementation is complete and awaiting owner/review acceptance, or review findings are
  still being resolved.
- `completed/`: accepted plans; review findings are fixed, accepted as debt, or explicitly waived.
- Long-lived technical debt is tracked in [reviews/tech-debt-tracker.md](../reviews/tech-debt-tracker.md).

## Naming Rule (New Plans)
- New files in `active/` must use `YYYY-MM-DD-<slug>-exec-plan.md`.
- This rule applies to newly created plans only; historical filenames stay unchanged.

## Lifecycle
1. Create a plan in `active/` with objective, done criteria, scope, constraints, phased tasks, and validation gates.
2. For newly created plans, include header metadata:
   - `Status:` with one of `active`, `in review`, `completed`, `cancelled`.
   - `Created:` with the creation date.
   - `Design:` linking to the design doc that owns durable contracts, omitted when no design doc exists.
   - `Scope:` listing the affected files, directories, or subsystems.
3. For newly created plans, include these sections:
   - `Objective`
   - `Done Criteria`
   - `Current Baseline`
   - `Implementation Decisions` (`None` is acceptable for small obvious changes)
   - `Phases`
   - `Validation Gates`
   - `Risks and Constraints` (`None` is acceptable when there are no known non-obvious risks)
   - `Out of Scope`
   - `Progress Notes` before moving the plan to `completed/`
4. Every phase must include an explicit `Purpose:` statement, `Status:` marker, and `Done when:`
   statement describing the business- or user-visible meaning of completion.
5. Use only these phase status values: `to be done`, `in progress`, `done`, `cancelled`.
6. `Done Criteria` and each phase's `Done when:` are the human-readable outcome layer — they state,
   in prose, what "done" means. `Validation Gates` are the executable, machine-checkable layer —
   they name executable commands or concrete inspection checks plus the expected outcome that prove
   the outcome layer is actually true. Both layers are required; neither substitutes for the other.
7. Execute work in phased increments; keep plan and phase status accurate during delivery.
8. Move the plan to `review/` when all non-cancelled phases are `done` and all in-scope validation
   criteria are met; set top-level `Status:` to `in review`.
9. Move the plan to `completed/` only after owner/review acceptance; set top-level `Status:` to
   `completed`.
10. If work is intentionally deferred, add an item to
    [reviews/tech-debt-tracker.md](../reviews/tech-debt-tracker.md) before closing the implementation.

Historical plans do not need to be rewritten to match this template.

## Readability Rules

Execution plans are for driving work. They should be easier to scan than the
design doc they implement: a maintainer should be able to see what will change,
what proves it is done, and which phase is next without reading an essay.

- Keep the plan narrower than the design doc. Link durable contracts instead of
  restating them; include only the context needed to execute safely.
- Keep `Scope` as a short list when it names more than two paths. Put doc
  updates in their own scope bullet instead of appending them to a long path
  sentence.
- `Objective` should state the intended end state and why this plan exists. Do
  not include phase tasks there.
- `Done Criteria` should be observable checks. Use one assertion per bullet, and
  put commands or required suites in their own bullet.
- `Current Baseline` should summarize the current code shape, not paste a full
  investigation log. If detailed measurements are required, use compact tables
  or move the raw detail to the relevant review/design artifact.
- `Implementation Decisions` should use `decision: rationale/effect` bullets.
  If a bullet needs more than 3 wrapped lines, split it into smaller decisions.
- Avoid large code sketches in plans. Prefer naming the function, signature, or
  test shape; put exact code in the implementation unless a tiny snippet prevents
  ambiguity.
- Every phase should be small enough to leave the tree green. If a phase has
  more than 8 substantial tasks, split it.
- Keep `Purpose:`, `Status:`, and `Done when:` to one short sentence each. If
  `Done when:` needs several checks, use bullets under the marker.
- Phase task lists should be imperative and concrete: edit this module, add this
  test, run this suite. Avoid explanatory paragraphs inside numbered tasks.
- Use `Progress Notes` for dated outcomes only. Do not append review transcripts,
  long calibration logs, or implementation diaries; link the review or report
  artifact when details matter.
- Use tables only for compact status matrices with short cells. If a cell needs
  a full sentence or multiple clauses, use bullets instead.
- Before finishing a plan edit, run a readability pass: no trailing whitespace,
  no lines over 120 characters, no giant paragraph, and no table that forces
  horizontal scrolling in a normal editor.

## Template

```md
# Exec Plan: Repository Boundary Cleanup

- Status: active
- Created: 2026-06-11
- Design: `docs/design-docs/example.md`
- Scope: `internal/example/`

## Objective

Move runtime assembly out of the persistence repository without changing generated output. Keep this short:
state the intended end state, not the full task list.

## Done Criteria

- Runtime assembly is owned by the service layer, not the repository.
- Generated output is unchanged for the covered fixture.
- Focused tests cover the new ownership boundary.

## Current Baseline

The repository currently owns both SQL reads and runtime assembly. This makes assembly hard to test without
database setup and blurs the persistence/service boundary.

## Implementation Decisions

- Keep SQL inside the repository; move only orchestration.
- Do not change schemas, query shape, or output formatting in this plan.

## Phases

### Phase 1: Repository Reads
Purpose: Expose the data reads needed by the service layer.
Status: to be done
Done when: the service can request raw repository data without receiving assembled runtime objects.

1. Add the repository read helper.
2. Add focused tests.

### Phase 2: Service Assembly
Purpose: Move runtime assembly into the service layer while preserving output.
Status: to be done
Done when: the repository no longer imports assembly helpers and the covered output fixture is unchanged.

1. Move orchestration calls to the service layer.
2. Update tests around the new ownership boundary.

## Validation Gates

- `go test ./internal/example -run TestRepositoryReads` passes.
- `go test ./internal/example -run TestServiceAssembly` passes.
- `rg -n "database/sql|SELECT " internal/example/service.go` returns nothing.

## Risks and Constraints

- Read consistency can change if a single connection is split into multiple repository calls.
- Avoid schema changes; they belong in a separate design and migration plan.

## Out of Scope

- Schema changes.
- Output format changes.

## Progress Notes

- Add dated notes before moving this plan to `completed/`, including any deviations from the plan above
  (scope changes, phases skipped or reordered, validation gates altered) and why.
```
