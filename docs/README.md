# Documentation Handbook

This directory separates portable documentation conventions from repository-specific catalogs.

## File Roles
- `README.md`: how to use a documentation area, including lifecycle rules, format rules, and reusable templates. README files should be portable across repositories with minimal edits.
- `index.md`: catalog and routing for the local repository. Index files list available documents and short descriptions; they should not carry reusable templates or process rules.
- Feature or subsystem documents: durable project knowledge, execution plans, runbooks, and review reports.

## Directory Conventions
- `design-docs/`: durable subsystem behavior, model contracts, ownership boundaries, and invariants.
- `exec-plans/`: phased implementation plans and lifecycle tracking.
- `operations/`: local setup, runbooks, smoke tests, and manual operational procedures.
- `reviews/`: review reports, implementation responses, and tracked long-lived technical debt.

## Update Rules
- Keep durable behavior changes close to their owning subsystem document.
- Update `index.md` when adding, moving, or retiring documents in that directory.
- Update the local directory `README.md` only when the reusable process, lifecycle, or document format changes.
- Do not put review reports, execution plans, or runbooks directly under `docs/`; use the routed subdirectories.
- Do not document real credentials or tokens. Use placeholders and link to `security.md`.

## Validation
Docs-only changes do not require tests unless the task explicitly asks for test output. Run `git diff --check` before
completion to catch markdown whitespace issues. Run `make check-docs` when changing `AGENTS.md`, `ARCHITECTURE.md`,
`README.md`, files under `docs/`, or either validator under `harness/`.
