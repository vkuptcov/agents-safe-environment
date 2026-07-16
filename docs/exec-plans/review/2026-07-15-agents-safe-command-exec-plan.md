# Exec Plan: Generic agents-safe Command Launcher

- Status: in review
- Created: 2026-07-15
- Design: [Safe Environment for Running Codex Agents](../../design-docs/codex-safe.md)
- Scope:
  - `cmd/agents-safe/`
  - `Makefile`
  - `tests/smoke/`
  - `ARCHITECTURE.md`, `README.md`, and the owning design doc

## Objective

Expose a public `agents-safe` binary that executes an explicit command in the existing isolated project session, so
`agents-safe bash` opens Bash without weakening the host or Docker isolation boundary.

## Done Criteria

- `make build` produces `bin/agents-safe`.
- `agents-safe bash` forwards `bash` as the container command without implicit shell interpretation.
- Missing commands and child exit statuses have clear, tested behavior.
- The real Sysbox suite proves the public Bash path starts in the selected project.
- User and design documentation describe the new command boundary accurately.

## Current Baseline

The session wrapper can execute arbitrary foreground commands, but only the smoke-tagged probe exposes that path.
The public `codex-safe` binary intentionally pins every invocation to the image-owned Codex executable.

## Implementation Decisions

- Add a separate public binary: keep `codex-safe` specialized to Codex and make command execution an explicit user
  choice through `agents-safe`.
- Reuse `launcher.Docker`: project discovery, mounts, lifecycle, and isolation stay identical to `codex-safe`.
- Pass command argv unchanged: no host shell, `eval`, string joining, or command rewriting.

## Phases

### Phase 1: Public command launcher

Purpose: Make an explicit container-command entry point available to users.
Status: done
Done when: `agents-safe bash` launches Bash through the shared launcher and invalid CLI input fails before launch.

1. Add `cmd/agents-safe` with the shared launcher options and a required command argv.
2. Add the binary to `make build`.
3. Add unit tests for direct argv forwarding, `--`, missing commands, and child exit codes.

### Phase 2: Runtime proof and documentation

Purpose: Preserve the container contract while documenting the new public surface.
Status: done
Done when: the real-host test and durable docs describe and prove the direct Bash path.

1. Add a smoke scenario that runs `agents-safe bash -c ...` without a separator.
2. Update the architecture map, product README, smoke README, and owning design doc.
3. Record validation results and move the plan to review.

## Validation Gates

- `gofmt -w cmd/agents-safe tests/smoke` leaves no formatting changes.
- `go test ./cmd/agents-safe ./internal/launcher` passes.
- `make test` passes.
- `make test-smoke-go` passes on a supported Sysbox host.
- `make check-docs` passes.
- `git diff --check` reports no whitespace errors.

## Risks and Constraints

- The command interface intentionally permits an arbitrary program inside the already-authorized container. It must
  not add host command execution, mounts, Docker-socket access, or shell interpolation.
- `agents-safe` keeps the Git-project preflight and the shared session and mount plan. The Codex-home coupling was
  later relaxed (see the 2026-07-16 progress note): `agents-safe` mounts the Codex home only when it already exists.

## Out of Scope

- Changing `codex-safe` argument behavior.
- Adding command allowlists, new mounts, or host-command execution.
- Persistent terminal attachment after a launcher process disconnects.

## Progress Notes

- 2026-07-15: Phase 1 added the public `agents-safe` binary, direct argv forwarding, and focused CLI tests.
- 2026-07-15: Phase 2 added the real-host `agents-safe bash` scenario and updated the architecture and user docs.
- 2026-07-15: `go test ./cmd/agents-safe ./internal/launcher`, `make test`, the focused real-host Bash scenario,
  and the full Sysbox smoke invocation completed successfully. Documentation and whitespace checks passed.
- 2026-07-16: The implementation review under `docs/reviews/feature-review/` drove follow-up changes:
  `agents-safe` now treats the Codex home as optional and `codex-safe` offers to create a missing default
  `~/.codex`; the shared CLI scaffold moved to `internal/cli` (+`clitest`); and the `codex-safe-probe` smoke
  transport was retired so the suite drives `agents-safe` directly. Unit tests and `go vet` pass on both modules;
  real-host `make test-smoke-go` and `make check-docs` are pending an environment with Sysbox/registry access.
- 2026-07-16: The Codex-home follow-up review hardened the optional policy, prompt stream/EOF behavior, deferred host
  directory creation until after Docker and reuse checks, added absent-home real-host coverage, and synchronized the
  user-facing and design contracts. See
  `docs/reviews/feature-review/2026-07-16-codex-home-policy-followup-review.md`.
- 2026-07-16: Follow-up validation passed: `make test`, launcher race tests, `make check-docs`, and the full
  `make test-smoke-go` retry. The first smoke run reproduced the existing cold-start `exit status 137` flake in
  `TestSysboxAgentsSafeBash`; the new absent-home scenario passed in both runs.
