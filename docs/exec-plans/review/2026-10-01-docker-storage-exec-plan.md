# Exec Plan: Persistent Nested Docker Storage

- Status: in review
- Created: 2026-10-01
- Design: [Safe environment](../../design-docs/agents-safe.md)
- Scope: Git discovery, launcher CLI/configuration, launch plan, Docker creation, fingerprint, tests and docs.

## Objective

Retain nested Docker state in named volumes selected by branch, project, or host-wide scope to avoid repeated pulls.

## Done Criteria

- All public launchers accept `--docker-storage=branch|project|shared`; TOML uses `common.docker_storage`.
- CLI overrides TOML; omitted settings select branch storage.
- Names limit project to 15 and branch to 20 characters, with a 12-character hash of complete identities.
- Creation mounts the selected volume and restart retains it; changed selections fail fingerprint validation.
- Required checks pass or concrete environment blockers are recorded.

## Current Baseline

The private daemon stores all persistent state in `/var/lib/docker` in the session writable layer.
Saved containers restart through `docker start` after fingerprint validation; mounts are immutable at creation.

## Implementation Decisions

- Use the primary checkout basename as project name so linked worktrees share project identity.
- Discover the symbolic branch, including unborn branches; use full commit SHA for detached HEAD.
- Hash full original names before normalization or truncation.
- Include scope and volume name in creation fingerprint schema 8.
- Permit concurrent shared mounts without locks, as explicitly requested by the owner.
- Preserve `--force-exec`: reuse original creation resources, including the volume.

## Phases

### Phase 1: Configuration and Identity
Purpose: Resolve a deterministic storage selection for every launcher.
Status: done
Done when: CLI and config select validated storage scope and bounded names.

1. Add failing config, discovery, naming, and CLI precedence tests.
2. Implement typed storage settings, branch discovery, and launch-plan naming.
3. Run affected package tests.

### Phase 2: Creation and Restart
Purpose: Retain state across container lifetimes without changing existing mounts during reuse.
Status: done
Done when: new containers mount selected storage and incompatible saved containers cannot restart normally.

1. Add creation and stopped-container mismatch tests.
2. Mount the writable named volume and fingerprint its scope/name.
3. Verify matching and forced restart paths.

### Phase 3: Documentation and Validation
Purpose: Document persistent state, configuration, and accepted concurrency risks.
Status: done
Done when: docs match behavior and required gates have recorded results.

1. Update architecture, owning design docs, public usage, and smoke coverage.
2. Run gofmt, affected tests, make test, make lint, make check-docs, and make test-smoke-go.
3. Move this plan to review after implementation and record results.

## Validation Gates

- Affected `go test` packages pass.
- `make test` and `make lint` pass.
- `make check-docs` passes.
- `make test-smoke-go` proves real Sysbox mounts, or its environment blocker is reported.
- `git diff --check` passes.

## Risks and Constraints

- Docker data-root sharing between concurrent daemons is unsupported; shared scopes are owner opt-ins.
- Branch names can change while a saved container exists; mismatch requires removing it or explicit force reuse.
- Existing container-local Docker state is not migrated; volumes begin empty.

## Out of Scope

- Dependencies, automatic migration, shared daemon, registry cache, and volume garbage collection.

## Progress Notes

- 2026-10-01: Owner approved three scopes, bounded names, config/CLI precedence, and branch default.
- 2026-10-01: CLI/TOML precedence, default branch scope, bounded names, creation mounts, and fingerprint schema 8
  implemented. Saved-container matching restart and changed-volume rejection are covered by unit tests.
- 2026-10-01: Added a real Sysbox test for nested image/volume persistence across restart and outer replacement.
  Smoke fixtures now use unique project names and clean their persistent Docker volumes.
- 2026-10-01: `make test`, `make lint`, and `make check-docs` passed. The test gate required expanded sandbox
  permissions for local sockets. Final checks were repeated after the Git tag-ambiguity fix.
- 2026-10-01: Real `make test-smoke-go` remains blocked: host Docker has no registered `sysbox-runc` runtime.
  The smoke module compiles and passes its ordinary gate with Sysbox scenarios skipped. No real mount claim is made.
