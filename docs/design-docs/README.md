# Design Docs

Design docs explain durable subsystem behavior: what problem the design solves,
what contract it creates, and how we know that contract still works. Keep them
short enough to read before changing the code.

## Core Beliefs

These conventions are mandatory for new implementation work unless an approved design explicitly overrides them.

### Engineering Conventions

- Preserve existing behavior unless an accepted design calls for behavioral change.
- Prefer small, reversible, test-backed changes over broad refactors.
- Keep module boundaries explicit; use the [Core Modules](../../ARCHITECTURE.md#core-modules) map to find the owning
  design before changing a runtime contract.
- Keep configuration explicit and typed; do not hide runtime assumptions.
- Maintain docs and code together when interfaces, security boundaries, or workflows change.

### Operational Conventions

- Never commit credentials or tokens.
- Run the smallest required suite from [testing.md](../testing.md) after changes; work is incomplete until that suite
  passes or an environment blocker is reported explicitly.

## Format
Start with this small shape. Add optional sections only when they carry real
contract value.

```md
# <Subsystem or Contract Name>

Status: <Proposed | Implemented | Deprecated>
Scope: <What this doc owns.>

## Purpose and Intent
<In plain language: what problem this solves, what approach we chose, what
tradeoff or alternative matters, and what success looks like.>

## Contract
<Inputs, outputs, stable behavior, persistence/API/model promises, and ownership
rules that callers or maintainers can rely on.>

## Boundaries and Non-Goals
<What this design owns, what consumes it, and what it deliberately does not do.>

## Test Plan
<Checks that prove the contract and catch regressions.>
```

## Format Rules
- Write the top section in plain language before domain jargon and implementation details.
- Lead the top section with the problem and a concrete worked example before any jargon:
  idea → before/after example → only then mechanics.
- Keep `file:line` and function references out of the top sections; collect them in a single
  `Where the code lives` section at the end if needed.
- Keep design docs about durable contracts, not implementation diaries or task lists.
- Prefer concrete invariants and file/module names over broad intent.
- Add `Runtime and Failure Behavior` only when sequencing, retries, external systems, consistency, or errors matter.
- Add `Invariants` only for hard rules that are not already clear in the contract.
- Add a Mermaid `sequenceDiagram` when runtime ordering, handoffs, retries, or failure paths are easier to
  understand visually than in prose.
- Add a Mermaid component diagram when module, service, storage, or integration boundaries are not obvious from the
  contract text.
- If a design change needs phased implementation, link the execution plan rather than embedding task tracking here.
- If no durable contract exists yet, add the design doc before or with the behavior change.

## Readability Rules
Design docs are working documents, not archival essays. A maintainer should be
able to skim the first screen, find the contract, and jump to code ownership
without parsing dense prose.

- Put `Status` on its own line. If `Scope` needs more than one clause, make it a
  short bullet list instead of one long paragraph.
- Structure `Purpose and Intent` as progressive disclosure:
  `Problem` → `Worked Example` → `Chosen Shape` → `Success Criteria` →
  `Tradeoff`. Use only the headings that carry real information.
- Keep one idea per paragraph. As a rule of thumb, a prose paragraph should be
  no more than 4 wrapped lines; split longer paragraphs into smaller paragraphs
  or bullets.
- Prefer a `term: explanation` bullet when a sentence contains a list of rules,
  cases, inputs, outputs, or cache effects. Do not pack multiple rules into one
  bullet.
- Keep bullets short: if a bullet needs more than 3 wrapped lines, promote it to
  its own subheading or split it into multiple bullets.
- Use `before` / `after` fenced blocks for behavioral contrasts. Do not bury a
  before/after example inside a paragraph.
- Use tables only for compact matrices with short cells. If any cell needs a
  full sentence or multiple clauses, use a bullet list of scenarios instead so
  the document does not require horizontal scrolling.
- Split `Contract` into numbered `###` sections with stable names. Start each
  section with the rule callers can rely on, then add mechanics and edge cases.
- Separate hard invariants from implementation mechanics. `Invariants` should
  contain only rules that must not regress, not a second copy of the whole
  design.
- For caches, retries, concurrency, staged execution, or failure isolation,
  prefer an ordered list or Mermaid diagram plus short bullets over a long
  narrative paragraph.
- Keep rejected alternatives in one place: either a short `Rejected alternative`
  paragraph in the relevant section or `Boundaries and Non-Goals`, not scattered
  through the document.
- Keep `Where the code lives` at the end. It is a code map, not the explanation;
  avoid `file:line` and function-name density before the contract is clear.
- Before finishing a doc, run a local readability pass: no trailing whitespace,
  no lines over 120 characters, no paragraph that visually dominates the screen,
  and no table that forces horizontal scrolling in a normal editor.

## Review and Plan Readability
These rules apply when writing or editing review reports, execution plans, and
other working docs under `docs/`, even when those docs have their own required
format.

- Preserve the required section order from the owning README, but make each
  section skimmable.
- Put long `Scope` values in bullets grouped by category, such as branch,
  baseline, docs, code, assets, and tests.
- Keep the first screen useful: verdict/status, confidence if relevant, and the
  shortest accurate summary of whether anything blocks the work.
- Use the findings table as an index only. Table cells should be short labels,
  not full explanations, scenarios, or suggested fixes.
- Put each finding's full explanation under its own detail heading. Use this
  order unless the owning format says otherwise:
  1. `File` or location.
  2. The observed problem.
  3. The concrete failure scenario or ambiguity.
  4. Why it matters.
  5. `Suggested fix`.
- Do not combine several review claims into one dense paragraph. Split verified
  behaviors, risks, and remaining work into separate bullets.
- Use fenced `before` / `after` or short `text` blocks when contrasting what a
  test currently proves with what it should prove.
- Keep recommendations actionable. Separate must-fix items, optional follow-up,
  and unrelated cleanup instead of compressing them into one sentence.
- If a report is read-only or tests were not run, state that once in
  `Verification` and avoid repeating it throughout the report.

## Mermaid Rules
Every ` ```mermaid ` block under `docs/` and in `ARCHITECTURE.md` is parsed by `make check-mermaid`
(folded into `make check-docs`).
Run it before you consider a doc done — a block that fails to parse renders as a blank error box
on GitHub.
The recurring breakages, all avoidable:
- **No `;` in `sequenceDiagram` message text.** `;` is a statement separator; `A->>B: did X; then Y` parses as
  a truncated message plus garbage and fails. Use a comma or dash.
- **Line breaks are `<br/>`, never `\n`.** A literal `\n` renders as the two characters on some renderers.
- **Escape `<` and `>` inside labels as `&lt;` / `&gt;`.** Raw angle brackets are read as HTML tags
  (e.g. write `&lt;doc&gt;`, not `<doc>`).
- **Quote any label with spaces or punctuation:** `A["extract card"]`, not `A[extract card]`.
- **Do not name a flowchart node `end`** (lowercase reserved word) — capitalize or rename it.
- **Balance every block opener with `end`:** `alt` / `opt` / `loop` / `par` / `subgraph`.

## Maintenance Rule
- If a change impacts architecture, runtime flow, persistence contracts, or integration behavior,
  update the relevant design doc or add one if missing.
