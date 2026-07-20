# Exec Plan: Session Bootstrap Readiness

- Status: in review
- Created: 2026-07-20
- Design: [`docs/design-docs/go-session-manager.md`](../../design-docs/go-session-manager.md)
- Scope:
  - `cmd/codex-safe-session/` readiness probe
  - `internal/launcher/` cold-session handoff
  - launcher and Sysbox smoke tests
  - session-manager design documentation

## Objective

Prevent a first unprivileged command from racing the root bootstrap that reconciles its account. A newly created
session must expose its command socket before the launcher runs the first user-owned `docker exec`.

## Done Criteria

- A cold session waits for root bootstrap and the session-manager socket before its first unprivileged command.
- Reused sessions keep their current direct command path.
- A failed bootstrap reports a bounded readiness failure without running a user command.
- Unit coverage pins root readiness before user exec, and the Sysbox smoke suite passes.

## Current Baseline

Detached `docker run` returns before `serve` has completed account reconciliation. The launcher immediately starts
`docker exec --user`, which can make `usermod` fail because that account already owns a process.

## Implementation Decisions

- Use a container-local, root-only readiness command that waits for the already-owned session socket.
- Keep the existing session protocol and user-command wrapper unchanged.
- Apply readiness only after a newly created session; reuse already requires a running managed container.

## Phases

### Phase 1: Readiness Contract
Purpose: Make completed root bootstrap observable without exposing host state.
Status: done
Done when: the container binary can wait for the manager socket and the design defines the ordering.

1. Add the socket-readiness helper and `codex-safe-session wait-ready` command.
2. Add unit tests for socket discovery, cancellation, and CLI dispatch.
3. Document the cold-session ordering contract.

### Phase 2: Launcher Handoff
Purpose: Block the first unprivileged exec until bootstrap is complete.
Status: done
Done when: every new session runs a root readiness exec before its first user command.

1. Add a bounded root readiness request after successful session creation.
2. Add launcher tests for the cold and reuse paths.
3. Preserve existing host-MCP readiness and cleanup behavior.

### Phase 3: Verification
Purpose: Prove the race is removed at the actual Sysbox boundary.
Status: done
Done when: focused checks and the complete smoke suite pass on this host.

1. Format changed Go files and run focused unit tests.
2. Run `make test`, `make check-docs`, and `make test-smoke-go`.

## Validation Gates

- `go test ./cmd/codex-safe-session ./internal/session ./internal/launcher` passes.
- `make test` passes.
- `make check-docs` passes.
- `make test-smoke-go` passes on the Sysbox host.
- `git diff --check` passes.

## Risks and Constraints

- The readiness command must run as root, before the user account is safe to enter.
- The wait must be bounded and must not register a managed command or alter the idle lifecycle.
- No new dependency is allowed.

## Out of Scope

- Resource limits, cache behavior, and project-image contents.
- Changes to the session-manager registration protocol.

## Progress Notes

- 2026-07-20: Created after real Sysbox smoke logs showed `usermod` racing the first user-owned exec.
- 2026-07-20: Added root `wait-ready`, cold-launch ordering coverage, and the session-manager contract update.
  `make test`, `make check-docs`, `git diff --check`, and `make test-smoke-go` passed on the Sysbox host.
