# Exec Plan: Docker CLI Transport Rollback

- Status: completed
- Created: 2026-07-16
- Design: [`agents-safe.md`](../../design-docs/agents-safe.md)
- Scope:
  - `internal/launcher/`
  - root and smoke Go modules
  - launcher design and package documentation

## Objective

Return host Docker orchestration to the Docker CLI so interactive attach, terminal, stream, and exit-status behavior
remain owned by the supported CLI instead of project code, while preserving the naming and file-ownership refactor.

## Done Criteria

- Production launcher operations invoke the host `docker` executable with validated argv.
- Root production dependencies contain no Moby client or API modules.
- Container lifecycle, mount, reuse, and command contracts remain unchanged.
- Required Go, smoke compilation, and documentation checks pass.

## Current Baseline

The uncommitted launcher migration uses low-level Moby create, attach, resize, stream-copy, and exec-inspect APIs.

## Implementation Decisions

- Retain the responsibility-oriented launcher file split.
- Restore CLI argument builders and subprocess error classification.
- Keep the existing Docker SDK in the real-host smoke harness; it is test-only orchestration.

## Phases

### Phase 1: CLI Transport
Purpose: Restore Docker CLI create, inspect, preflight, and exec operations.
Status: done
Done when: production launcher code no longer imports Moby or implements attached exec transport.

1. Restore validated `docker run` and `docker exec` argv builders.
2. Restore subprocess runner diagnostics and inspect JSON parsing.
3. Remove Moby-specific terminal and stream handling.

### Phase 2: Dependencies and Tests
Purpose: Return dependency and test boundaries to the CLI implementation.
Status: done
Done when: root dependencies are back to the pre-migration graph and focused CLI tests pass.

1. Remove root Moby dependencies.
2. Restore CLI-oriented launcher tests.
3. Restore the previous smoke SDK module graph.

### Phase 3: Documentation and Validation
Purpose: Make the durable contract match the restored implementation.
Status: done
Done when: design docs describe the host Docker CLI and all required gates pass.

1. Update package and design documentation.
2. Mark the Moby migration plan cancelled.
3. Run repository and documentation gates.

## Validation Gates

- `GOCACHE=/tmp/agents-safe-go-build make test` passes.
- `make check-docs` passes.
- `git diff --check` passes.
- `rg -n "moby/moby|containerd/errdefs" go.mod go.sum internal/launcher` returns no matches.
- `rg -n "os/exec|exec\\.Command" internal/launcher` finds the Docker CLI subprocess adapter.

## Risks and Constraints

- CLI error classification depends on stable Docker diagnostics already covered by focused tests.
- Preserve unrelated uncommitted naming, README, and terminology changes.

## Out of Scope

- Changing container reuse or concurrent-create behavior.
- Replacing the test-only Docker SDK in the smoke harness.
- Changing nested Docker behavior inside the container.

## Progress Notes

- 2026-07-16: Owner requested rollback after reviewing the additional attached-exec responsibilities.
- 2026-07-16: Restored Docker CLI argv, subprocess execution, inspect parsing, and diagnostic classification.
- 2026-07-16: Removed root Moby dependencies while retaining the test-only Docker SDK in the smoke module.
- 2026-07-16: `make test`, `make check-docs`, and the complete real Sysbox smoke suite passed.
