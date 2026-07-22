# Exec Plan: agents-safe Container Labels

- Status: in review
- Created: 2026-07-22
- Design: `docs/design-docs/go-session-manager.md`
- Scope:
  - session and host-MCP Docker label keys in `internal/launcher/`
  - launcher and smoke assertions for those labels
  - current operator and design documentation

## Objective

Move the common Docker-label namespace from `codex-safe.*` to `agents-safe.*`. The labels identify one shared
session used by Codex, Claude Code, and arbitrary commands, so the namespace must describe that shared boundary.

## Done Criteria

- New sessions and host-MCP sidecars emit only `agents-safe.*` labels.
- Reuse and cleanup filters use the same new namespace.
- Current docs and smoke assertions show the new label contract.
- An existing `codex-safe.*` container is rejected as a deterministic-name conflict rather than reused.

## Current Baseline

The three public launchers share one managed Sysbox session, but its ownership, fingerprint, cache, state, and
host-MCP labels all use `codex-safe.*`. That makes generic `agents-safe` and `claude-safe` launches appear to belong
to Codex.

## Implementation Decisions

- No dual-read compatibility: old labels fail ownership validation, preventing a new launcher from reusing a
  container whose creation contract it cannot prove.
- Keep product-specific names and paths unchanged: Codex and Claude executables, their volumes, and their public
  launchers remain product-owned.
- Do not rewrite completed or review execution plans: they are historical records; only the live contract docs move.

## Phases

### Phase 1: Shared Runtime Contract
Purpose: Change the authoritative label namespace at every session and sidecar creation/reuse boundary.
Status: done
Done when: the launcher emits, validates, and filters only `agents-safe.*` keys.

1. Rename common label constants and host-MCP labels.
2. Update launcher and smoke assertions, Docker filters, and operator examples.
3. Record the intentional old-container rejection in the owning design docs.

### Phase 2: Validation and Handoff
Purpose: Prove the migrated contract and leave the plan ready for review.
Status: done
Done when: focused and repository checks pass, and the plan records the result.

1. Format Go sources and run affected launcher tests.
2. Run the repository test, lint, and documentation gates.
3. Move this plan to `review/` and update the catalog.

## Validation Gates

- `gofmt -w` leaves changed Go files formatted.
- `go test ./internal/launcher/...` passes.
- `make test` passes.
- `make lint` passes.
- `make check-docs` passes.
- `rg -n 'codex-safe\\.(managed|project-path|host-uid|manager-protocol|launch-config|host-mcp)'` finds only the
  explicit legacy-rejection test outside historical plans.

## Risks and Constraints

- A running container created with old labels cannot be reused after upgrade. It must exit or be explicitly stopped
  before a replacement is created; this preserves the deterministic-name and ownership safety boundary.

## Out of Scope

- Renaming public `codex-safe` or `claude-safe` commands.
- Renaming product executable volumes, executable paths, or product-state labels.
- Renaming generic container names, the session wrapper command, images, and runtime socket paths; these need a
  separate migration because they are broader externally visible identifiers.

## Progress Notes

- 2026-07-22: Created after inventory confirmed that all current labels identify the shared session, not Codex alone.
- 2026-07-22: Replaced session, cache, host-MCP sidecar, and smoke labels with `agents-safe.*`; old ownership labels
  are rejected deliberately. `go test ./internal/launcher/...`, `make test`, `make lint`, and `make check-docs` pass.
