# Exec Plan: Forced Exec into an Active Session

- Status: in review
- Created: 2026-07-23
- Design: `docs/design-docs/agents-safe.md`
- Scope:
  - `internal/cli/`, `internal/launcher/`
  - `cmd/codex-safe/`, `cmd/claude-safe/`, `cmd/agents-safe/`
  - public and design documentation

## Objective

Add one explicit emergency override that lets all three launch commands execute in an owned, protocol-compatible
running container even when its creation fingerprint differs from the current launch plan.

## Done Criteria

- `codex-safe`, `claude-safe`, and `agents-safe` accept `--force-exec` before product or command arguments.
- Without the flag, fingerprint mismatch remains fail-closed and its error names the override.
- With the flag, the launcher reuses the existing container without changing its creation-time resources.
- Ownership and manager-protocol validation remain mandatory.
- Focused, repository-wide, lint, and documentation gates pass.

## Current Baseline

The shared CLI parses all launcher flags and passes resolved launch options to one Docker lifecycle. That lifecycle
rejects a running container when `agents-safe.launch-config` differs, and the current diagnostic only asks the user
to finish the active session.

## Implementation Decisions

- Keep `--force-exec` invocation-only; it is neither project configuration nor a fingerprint input.
- Bypass only the creation-fingerprint predicate, after ownership and protocol checks.
- On a forced mismatch, use the running container as-is and skip creation-time host-MCP reconciliation.
- Emit a warning with both fingerprints whenever the override actually takes effect.

## Phases

### Phase 1: Shared CLI and Lifecycle
Purpose: Route the explicit override through all launch commands and narrowly relax active-container adoption.
Status: done
Done when: all three commands can force an exec while default mismatch handling remains strict.

1. Add and document the shared flag.
2. Carry the invocation-only option into the launcher.
3. Add strict and forced mismatch tests, including no creation-time reconciliation.

### Phase 2: Durable Contract and Validation
Purpose: Make the emergency behavior discoverable and prove the changed lifecycle boundary.
Status: done
Done when: public/design docs match the implementation and all required gates pass.

1. Update the owning design docs and README command surfaces.
2. Run formatting, focused tests, `make test`, `make lint`, and `make check-docs`.
3. Move this plan to review with validation outcomes.

## Validation Gates

- `go test ./internal/cli ./internal/launcher ./cmd/codex-safe ./cmd/claude-safe ./cmd/agents-safe` passes.
- `make test` passes.
- `make lint` passes.
- `make check-docs` passes.

## Risks and Constraints

- The forced command sees the running container's old mounts, image, cache routing, and host-MCP state.
- The override must never weaken deterministic-name ownership or manager-protocol checks.

## Out of Scope

- Reconfiguring a running container.
- Persisting the override in `.agents-safe/config.toml`.
- Stopping or replacing an active container.

## Progress Notes

- 2026-07-23: Plan created; the shared CLI and lifecycle are the only runtime routing points.
- 2026-07-23: Added shared parsing, strict and forced lifecycle paths, as-is host-MCP adoption, and command/launcher
  tests.
- 2026-07-23: Focused tests, `make test`, `make lint`, and `make check-docs` passed. The real Sysbox suite passed in
  271.735 seconds, including strict mismatch diagnostics and forced reuse of the same container ID.
