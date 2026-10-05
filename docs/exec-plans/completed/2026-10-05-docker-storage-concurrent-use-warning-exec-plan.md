# Docker Storage Concurrent-Use Warning

Status: completed
Created: 2026-10-05
Design: [agents-safe.md](../../design-docs/agents-safe.md)
Scope:
- `internal/launcher/dockercli/volume.go`
- `internal/launcher/container_lifecycle.go`
- launcher tests
- docs: `docs/design-docs/agents-safe.md`

## Objective

Before a launch starts a nested Docker daemon, print a stderr warning when the selected storage volume is already
mounted by another running container. Concurrent use stays the developer's responsibility: the launch proceeds.

## Done Criteria

- Cold create and persistent restart warn and name the other running containers that mount the storage volume.
- No warning is printed when no other running container mounts the volume.
- The launch continues after the warning.
- Reusing an already running session does not check or warn.
- `make test` and `make check-docs` pass.

## Current Baseline

`ensureDockerStorage` creates the volume before cold create; `restartContainer` starts a stopped persistent session.
Neither looks at other users of the volume, so a second daemon on a shared data-root fails inside the container
(containerd boltdb lock timeout) and the host sees only a readiness exec exit status.

## Implementation Decisions

- `docker ps --filter volume=<name>`: lists only running containers and matches the exact volume name.
- Exclude this session's own container name: a restarted container is stopped at check time, but the filter stays
  correct if that changes.
- Warn only where this launch starts a daemon: an already running session has its daemon and lock already.
- A failed listing fails the launch: it is the same host Docker CLI the next create/start needs.

## Phases

### Phase 1: Warning
Purpose: make concurrent storage use visible before the second daemon starts.
Status: done
Done when: cold create and restart print the warning for a volume used by another running container.

1. Add `Client.RunningContainersUsingVolume` in `dockercli`.
2. Add `launchAttempt.warnConcurrentDockerStorage` and call it after `ensureDockerStorage` and before restart `Start`.
3. Add launcher tests for warning, no warning, own-name exclusion, and restart.
4. Update the storage section of `agents-safe.md`.

## Validation Gates

- `gofmt -l internal/` prints nothing.
- `make test` passes.
- `make check-docs` passes.

## Risks and Constraints

None.

## Out of Scope

- Locks, refusal to start, or prompts on concurrent use.
- Surfacing container logs when readiness fails.

## Progress Notes

- 2026-10-05: plan created.
- 2026-10-05: implemented; `make test`, `make lint`, `make check-docs` pass. Restart reads the volume from the
  inspected creation-time mount, so `--force-exec` with a changed selection checks the volume actually started.
