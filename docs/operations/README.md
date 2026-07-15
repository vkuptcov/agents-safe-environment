# Operations Docs

Operational docs cover local setup details, external credentials guidance, manual smoke tests, and command procedures that do not belong in subsystem design docs.

## Runbook Format
Workflow-specific runbooks and smoke-test guides should use this structure:

````md
# <Workflow or Smoke Test Name>

## Purpose
<What this procedure validates or operates.>

## When To Use
<The code paths, incidents, releases, or manual checks that require this runbook.>

## Prerequisites
- <Local services, credentials, data, or external access required.>

## Command
```bash
<single primary command, when one exists>
```

## Procedure
1. <Step with exact command, URL, or file path.>
2. <Step with expected intermediate signal.>

## Success Criteria
- <Observable result that means the procedure passed.>

## Failure Signals
- <Common failure and what it usually means.>

## Cleanup
<How to stop services, remove test data, or restore state. Omit when not applicable.>
````

## Runbook Rules
- Keep credentials as variable names or placeholders only.
- Link back to `../security.md` when credentials or tokens are involved.
- Prefer exact commands from `../makefile-reference.md`.
- Keep live external checks separate from default automated test policy.
