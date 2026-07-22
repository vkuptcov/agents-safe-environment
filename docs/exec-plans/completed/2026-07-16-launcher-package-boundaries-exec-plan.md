# Exec Plan: Launcher Package Boundaries

- Status: completed
- Created: 2026-07-16
- Design: [`agents-safe.md`](../../design-docs/agents-safe.md)
- Scope:
  - `internal/launcher/`
  - `internal/cli/` and launcher entrypoints
  - architecture and package documentation

## Objective

Separate the validated launch plan and host Docker CLI adapter from launcher orchestration so package ownership is
clear without changing container lifecycle, mount, terminal, or retry behavior.

## Done Criteria

- `internal/launcher/launchplan` owns the Git-project-to-launch-plan contract.
- `internal/launcher/dockercli` owns Docker argv, subprocess execution, inspect parsing, and transport diagnostics.
- `internal/launcher` owns container identity, lifecycle, reuse, user mounts, and retry policy.
- Package dependencies are one-way and contain no import cycle or parent-package import from either child package.
- Required Go, smoke-compilation, and documentation gates pass.

## Current Baseline

Plan construction, Docker argv, subprocess transport, inspect parsing, and launcher lifecycle are separate files but
share one `launcher` package. The combined Docker test file covers both transport details and lifecycle policy.

## Implementation Decisions

- Name the child packages `launchplan` and `dockercli`; avoid a generic `model` or `docker` package.
- Give `dockercli` request types tailored to Docker operations instead of passing the complete launch plan.
- Keep deterministic naming, label expectations, reuse waits, and retry decisions in `launcher`.
- Preserve the existing CLI implementation and test-only Docker SDK boundary.

## Phases

### Phase 1: Launch Plan Package
Purpose: Give the validated filesystem contract a focused package and stable dependency direction.
Status: done
Done when: callers use `launchplan.Plan`, and plan construction and validation tests live with that package.

1. Move plan types, construction, normalization, and path validation into `internal/launcher/launchplan`.
2. Update CLI, command, test-helper, launcher, and test signatures.
3. Add a concise package README.

### Phase 2: Docker CLI Package
Purpose: Isolate Docker-specific request encoding and host subprocess behavior.
Status: done
Done when: `launcher` invokes typed Docker operations and contains no `os/exec`, Docker inspect JSON, or argv builder.

1. Add typed create, inspect, preflight, and exec operations under `internal/launcher/dockercli`.
2. Move subprocess diagnostics and Docker-specific validation into the adapter.
3. Split transport tests from lifecycle-policy tests.

### Phase 3: Documentation and Validation
Purpose: Make the documented module map and verification evidence match the new ownership.
Status: done
Done when: package docs and architecture describe the split and all required gates pass.

1. Update launcher and architecture documentation.
2. Run focused package tests, `make test`, and `make check-docs`.
3. Move this plan to `review/` with final progress notes.

## Validation Gates

- `go test ./internal/launcher/... ./internal/cli/...` passes.
- `go vet ./internal/launcher/... ./internal/cli/...` passes.
- `GOCACHE=/tmp/agents-safe-go-build make test` passes.
- `make check-docs` passes.
- `git diff --check` passes.
- `go list -deps ./internal/launcher | rg "internal/launcher/(launchplan|dockercli)"` finds both child packages.
- `rg -n '"os/exec"|json\\.Unmarshal' internal/launcher/*.go` returns no matches.

## Risks and Constraints

- Preserve the existing uncommitted naming, README, and Docker CLI rollback changes.
- Do not move launcher lifecycle policy into the transport package.
- Do not add dependencies or change the real Sysbox contract.

## Out of Scope

- Changing Docker CLI behavior or replacing it with an SDK.
- Changing managed-container labels, reuse policy, mounts, or retry semantics.
- Reworking the smoke harness Docker client.

## Progress Notes

- 2026-07-16: Owner approved the `launchplan` plus `dockercli` package split.
- 2026-07-16: Moved plan construction and validation into `launchplan`; all callers use `launchplan.Plan`.
- 2026-07-16: Moved Docker argv, subprocesses, inspect parsing, and transport classification into `dockercli`.
- 2026-07-16: `make test`, `make check-docs`, and the complete real Sysbox smoke suite passed.
