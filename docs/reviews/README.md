# Reviews

Review artifacts live under `docs/reviews/` and are split by lifecycle.

## Layout

- `feature-review/`: local-only reports for active feature work. This directory is ignored by git. Use it for
  findings that should be resolved or explicitly accepted before the feature is done.
- `code-review/`: tracked general review reports that are part of project history and may seed future tasks.
- `tech-debt-tracker.md`: tracked long-lived debt and follow-ups that are not tied to completing the current feature.

## Process

1. For active feature review requests, write the detailed report to `docs/reviews/feature-review/` by default
   and keep chat output to the essential findings and file path.
2. For broader project/codebase reviews, write the report to `docs/reviews/code-review/`.
3. When addressing review findings, update the same review report with implementation responses instead of
   leaving fix notes only in chat.
4. Promote only durable deferred work to `tech-debt-tracker.md`.
5. Do not place review reports directly under the `docs/` root; keep them under `docs/reviews/`.
6. Follow the report format below for every new review report, regardless of which agent writes it.

## Feature Review Cycle

For non-trivial or high-risk feature work, use a three-stage review cycle when requested or when risk warrants it:

1. Design review before the execution plan is written.
2. Plan review before implementation starts.
3. Implementation review after the code/docs changes are ready.

Use these default report names in `docs/reviews/feature-review/`:
- `YYYY-MM-DD-<slug>-design-review.md`
- `YYYY-MM-DD-<slug>-plan-review.md`
- `YYYY-MM-DD-<slug>-implementation-review.md`

This cycle is not required for small, obvious changes. If a stage is skipped for a non-trivial change, note why
in the plan or implementation report.

## Report Format

Every review report uses the same structure:

```md
# <Feature/Scope> Review

Date: YYYY-MM-DD HH:MM

Scope:

- Branch/baseline: <branch, commit, PR, or working-tree range>
- Reviewed artifact: <diff, plan, design doc, implementation, or subsystem>
- Relevant docs: <docs used as source of truth, if any>
- Relevant tests: <test files or suites in scope, if any>

Reviewer: <agent name>

## Code Review

**Verdict**: APPROVE | APPROVE WITH NOTES | NEEDS DECISION | REQUEST CHANGES
**Confidence**: LOW | MEDIUM | HIGH

### Summary

<short verdict-oriented summary: blockers first, then the main risk area or why the work is safe>

### Findings

| Priority | ID | Status | Issue | Location |
|----------|----|--------|-------|----------|
| P1 | F-001 | Open | <short issue label> | `path/to/file.go:123` |

### Details

#### [P1] F-001: <short title>

**File:** `path/to/file.go:123`

Observed problem:

- <what is wrong>

Concrete failure scenario:

- <how this fails or becomes ambiguous>

Why it matters:

- <impact on correctness, security, performance, operations, or maintainability>

**Suggested fix:**

<narrow direct change that fixes this finding>

### Verification

- <command or read-only check> -> <result>
- If tests were not run, state why once here.

### Recommendation

<what to fix before merge, what can follow>
```

Conventions:

- Priorities: `P1` (must fix before merge), `P2` (should fix), `P3` (polish/cleanup). Findings are ordered
  most severe first.
- The `Status` column starts as `Open`; it is updated when the finding is addressed.
- `Location` uses `file:line` so findings are clickable and verifiable.
- Verified claims beat speculation: reproduce a finding with a command or quote the code when possible, and say
  so under `Verification`.
- Suggested fixes are the narrowest direct change that fixes the stated problem: exactly one fix per finding.
  Before writing it, check what the touched code actually uses.
- Do not attach `and/or` alternatives, optional refactors, shared abstractions for a couple of call sites, or
  speculative work for future scenarios. If such work is worth doing, create a separate finding or tech-debt entry.

## Readability Rules

Review reports are working documents. A maintainer should understand the
verdict, blockers, and next action from the first screen.

- Keep the required section order from [Report Format](#report-format).
- Put long `Scope` values in bullets grouped by category:
  - branch/baseline
  - reviewed artifact
  - relevant docs
  - code areas
  - tests
- Keep `Summary` short:
  - first paragraph: verdict and blockers
  - second paragraph: main verified safety/correctness conclusion
  - third paragraph: non-blocking risks, if any
- Use the findings table as an index only:
  - `Issue` is a short label, not a full explanation
  - `Location` is the primary `file:line`; put secondary call sites in `Details`
  - do not put suggested fixes, scenarios, or multi-clause explanations in table cells
- Put the full explanation for each finding under its own detail heading.
- Use this order for each finding detail:
  1. `File` or location.
  2. `Observed problem`.
  3. `Concrete failure scenario` or ambiguity.
  4. `Why it matters`.
  5. `Suggested fix`.
- Use bullets for multiple facts, paths, affected cases, or verification points.
- Keep one idea per paragraph. Split paragraphs that visually dominate the screen.
- Use fenced `text`, `before`, `after`, or language-specific blocks for examples and commands.
- If a command is long, use a fenced multi-line shell block instead of one long inline command.
- State "tests not run" once in `Verification` with the reason; do not repeat it in every finding.
- Keep `Recommendation` actionable:
  - must-fix before merge
  - optional follow-up
  - accepted or deferred work
- Before finishing a report, run a local readability pass:
  - no trailing whitespace
  - no lines over 120 characters
  - no table that requires horizontal scrolling in a normal editor
  - no finding detail compressed into one dense paragraph

## Verdicts

Use verdicts consistently:
- `APPROVE`: no blocking findings.
- `APPROVE WITH NOTES`: no blocking findings, but there are non-blocking follow-ups or residual risks.
- `NEEDS DECISION`: implementation should pause until a product, architecture, or ownership decision is made.
- `REQUEST CHANGES`: one or more blocking findings must be fixed before proceeding.

Repeat review cycles may add `## Re-review Verification` to state what changed and what was rechecked.

Implementation responses use the protocol below.

## Implementation Responses

Review reports are working artifacts. Preserve the original findings as the source of truth for what was
reviewed, and append or update a `## Implementation Responses` section when fixes are made.

Use stable finding IDs when possible (`F-001`, `F-002`, or `P1-001`). If a report does not have IDs yet, add
them without changing the finding substance.

Each response should include:

- Finding ID.
- Status: `Fixed`, `Rejected`, `Accepted as debt`, or `Needs follow-up`.
- Short response describing the change or decision.
- Changed files or docs.
- Verification command or reason verification was not run.

Template:

```md
## Implementation Responses

Updated: YYYY-MM-DD HH:MM
Agent: <agent name>
Branch/commit: <branch, commit, or current working tree>

| Finding | Status | Response | Verification |
| --- | --- | --- | --- |
| F-001 | Fixed | Added explicit failure handling in `internal/foo/bar.go`. | `go test ./internal/foo` |
| F-002 | Accepted as debt | Deferred to `docs/reviews/tech-debt-tracker.md`. | N/A |

### Notes

Add any cross-finding implementation notes here.
```

After all findings are resolved or explicitly accepted, optionally add a short top-of-file resolution note, for example:

```md
> **Resolution (YYYY-MM-DD HH:MM):** all findings addressed; F-002 was accepted as debt and promoted to
> `docs/reviews/tech-debt-tracker.md`.
```
