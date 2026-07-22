# Exec Plan: agents-safe Runtime Namespace

- Status: in review
- Created: 2026-07-22
- Design: `docs/design-docs/agents-safe.md`
- Scope:
  - common container runtime binary, names, images, volumes, paths, and MCP environment contract
  - generic runtime design doc and references
  - Docker build, launcher, unit tests, smoke assertions, and operator documentation

## Objective

Rename every shared runtime identifier from `codex-safe` to `agents-safe`. The runtime serves Codex, Claude Code, and
arbitrary commands, while `codex-safe` and `claude-safe` remain the two public product launch commands.

## Done Criteria

- Sessions, sidecars, images, project images, runtime paths, and host-MCP environment names use `agents-safe`.
- Codex and Claude installation volumes use `agents-safe-*` names and mount below `/opt/agents-safe/`.
- The image runs `agents-safe-session`; product update commands remain `codex-safe update` and `claude-safe update`.
- No compatibility layer reads the old identifiers.
- Existing matching containers and volumes are explicitly removed before Docker validation.

## Current Baseline

The common runtime still uses `codex-safe` in its session binary, deterministic container and sidecar names, image tags,
project-image tags, socket directories, host-MCP variable, volumes, and install roots. This predates `claude-safe` and
mislabels the shared runtime.

## Implementation Decisions

- Clean break: no migration, aliases, or dual-read logic. Existing `codex-safe*` containers and named volumes are
  removed, then the new image initializes fresh `agents-safe-*` volumes.
- Product surface stays product-specific: retain `codex-safe`, `claude-safe`, `codex-safe-update`,
  `claude-safe-update`, `CODEX_HOME`, and Claude's native environment names.
- Rename the generic design doc to `agents-safe.md`; historical plan filenames and prose remain historical records.

## Phases

### Phase 1: Runtime Namespace
Purpose: Change all common runtime identifiers as one internally consistent contract.
Status: done
Done when: the launcher, image, container manager, installation mounts, and MCP channel agree on `agents-safe`.

1. Rename the session command/package and common Docker names, tags, volumes, paths, and environment variable.
2. Update image construction, launch/update requests, unit and smoke assertions.
3. Rename the generic design doc and update current docs and links.

### Phase 2: Clean Docker State and Validation
Purpose: Prove the new clean-break runtime is usable from a fresh Docker state.
Status: done
Done when: legacy resources are absent and focused, repository, Docker, documentation, and smoke gates pass.

1. Inspect, then remove only exact legacy `codex-safe*` session containers and installation volumes.
2. Run formatting, tests, lint, image build, docs, and real-host smoke validation.
3. Move this plan to `review/` with the recorded cleanup and validation result.

## Validation Gates

- `gofmt -w` leaves changed Go files formatted.
- `go test ./cmd/... ./internal/...` passes.
- `make test` and `make lint` pass.
- `make docker-build` creates and initializes `agents-safe-codex` and `agents-safe-claude`.
- `make check-docs` passes.
- `make test-smoke-go` passes on the Linux Sysbox host.
- `docker ps -a` and `docker volume ls` contain no legacy exact resources after cleanup.

## Risks and Constraints

- Removing volumes discards installed Codex and Claude Code releases. The next Docker build must fetch and install them.
- Renaming the session binary and installation roots changes the image-entrypoint and mount contract together; partial
  changes cannot be exercised safely.

## Out of Scope

- Renaming the public `codex-safe` and `claude-safe` commands.
- Migrating existing sessions, volumes, or old image tags.
- Rewriting historical execution plans solely to update old names.

## Progress Notes

- 2026-07-22: Docker inventory returned no legacy `codex-safe*` container or volume. The user approved clean removal
  rather than migration for any resources that appear before validation.
- 2026-07-22: Renamed the shared session command, images, containers, sidecars, volumes, paths, and host-MCP contract;
  product launch and update commands remain `codex-safe` and `claude-safe`. `go test ./cmd/... ./internal/...`,
  `make test`, `make lint`, `make build`, and `make check-docs` pass.
- 2026-07-22: Re-ran `make docker-build` with Docker access after the base layer was cached. It built
  `agents-safe-mvp:local`, initialized Codex 0.145.0 and Claude Code 2.1.217 in the new volumes, and a read-only
  mount proof ran both executables. The full `TestSysbox` gate and focused generic, product-coexistence, and host-MCP
  scenarios passed.
- 2026-07-22: Removed the inspected legacy `codex-safe-*` session/sidecar containers and `codex-safe-codex` plus
  `codex-safe-claude` volumes. Fresh `agents-safe-codex` and `agents-safe-claude` volumes remain.
