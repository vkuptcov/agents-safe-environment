# Nested Docker overlay2 Graph Driver

Status: completed
Created: 2026-10-05
Design: [agents-safe.md](../../design-docs/agents-safe.md)
Scope:
- `internal/container/dockerd.go` and its test
- docs: `docs/design-docs/agents-safe.md`

## Objective

Stop storing every nested image twice. The nested daemon uses the classic `overlay2` graph driver instead of Docker's
default containerd image store, and the decision with its rejected alternatives is recorded in the design doc.

## Done Criteria

- Nested `dockerd` starts with `--storage-driver=overlay2`.
- `docker info` inside a Sysbox session reports `overlay2` on fresh and previously used data-roots.
- Named-volume data written under the containerd store stays readable after the switch.
- The design doc records the decision.
- `make test`, `make lint`, and `make check-docs` pass.

## Current Baseline

`dockerDaemonArguments` passes no storage options, so Docker 29 defaults to the containerd image store
(`driver=overlayfs`). A measured shared volume held 1.2G of compressed content beside 3.7G of unpacked layers.

## Implementation Decisions

- Pass flags, not `daemon.json`: all daemon options already live in `dockerDaemonArguments` and its test.
- `--storage-driver=overlay2` alone: it disables the containerd image store on fresh and previously used data-roots;
  no `containerd-snapshotter` feature flag is needed.
- No automatic migration: superseded by versioned storage volume names.

## Phases

### Phase 1: Driver switch
Purpose: halve per-image disk use of nested Docker.
Status: done
Done when: nested daemons run on `overlay2` and the decision is documented.

1. Add `--storage-driver=overlay2` to `dockerDaemonArguments`; update the argument test.
2. Verify in a real Sysbox container: fresh data-root, data-root first used by the containerd store, named-volume
   data survival, and `ctr` reclaim.
3. Update the Inner Docker Contract and rejected alternatives in `agents-safe.md`.
4. Add a migration runbook (later replaced by versioned storage volumes, see
   [the storage format plan](2026-10-05-docker-storage-format-exec-plan.md)).

## Validation Gates

- `go test ./internal/container` passes.
- `make test`, `make lint`, `make check-docs` pass.
- Manual Sysbox check: `docker info --format '{{.Driver}}'` prints `overlay2`; a file written to a named volume before
  the switch is readable after it; `ctr -n moby images rm --sync` shrinks `containerd/daemon` content.

## Risks and Constraints

- Superseded by versioned storage volumes: format-2 sessions start on fresh volumes and never open format-1 data.

## Out of Scope

- Host filesystem deduplication across concurrent data-roots.
- Automatic cleanup of containerd-store data in existing volumes.

## Progress Notes

- 2026-10-05: implemented and verified in Sysbox with Docker 29.1.3: fresh data-root `overlay2`; a data-root
  initialized by the containerd store switches to `overlay2`, keeps named-volume data, and hides old images;
  `ctr ... images rm --sync` reclaimed content from 12M to 72K.
