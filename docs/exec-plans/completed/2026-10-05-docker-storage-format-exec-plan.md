# Versioned Nested Docker Storage Volumes

Status: completed
Created: 2026-10-05
Design: [agents-safe.md](../../design-docs/agents-safe.md)
Scope:
- `internal/launcher/launchplan/docker_storage.go` and its test
- docs: `docs/design-docs/agents-safe.md`

## Objective

Let sessions on images with different nested-daemon storage formats run side by side. The storage format version is
part of every storage volume name, so a format change starts on fresh volumes and never opens old-format data.

## Done Criteria

- Branch, project, and shared volume names end in `-v2`.
- A persistent container created with an older volume reports the storage mismatch with a format-change hint.
- `make test`, `make lint`, and `make check-docs` pass.

## Current Baseline

Volume names depend only on scope, project, user, and branch. Switching the nested daemon to `overlay2` reuses the same
volumes, hiding their containerd-store images while leaving the data on disk.

## Implementation Decisions

- `launchplan.DockerStorageFormat` constant: mirrors `session.ProtocolVersion`; launcher and image share one source.
- Readable `-v<format>` suffix rather than hashing the version: retained volumes stay attributable by name.
- Unsuffixed volumes are format 1: existing volumes are neither renamed nor touched.
- No data migration, including nested named volumes: owner decision.
- The migration runbook for reclaiming containerd-store data is removed: new daemons never open old volumes.

## Phases

### Phase 1: Versioned names
Purpose: isolate storage formats by volume name.
Status: done
Done when: every scope resolves a `-v2` volume and the design doc describes the versioning.

1. Add `DockerStorageFormat` and the name suffix in `resolveDockerStorage`; update naming tests.
2. Extend the storage mismatch hint.
3. Update the storage section of `agents-safe.md`; remove the migration runbook.

## Validation Gates

- `go test ./internal/launcher/...` passes.
- `make test`, `make lint`, `make check-docs` pass.

## Risks and Constraints

- Nested images and named-volume data in format-1 volumes are not visible to format-2 sessions.

## Out of Scope

- Copying images or named-volume data between formats.
- Automatic removal of old-format volumes.

## Progress Notes

- 2026-10-05: implemented; gates pass.
- 2026-10-05: dropped the planned `agents-safe.docker-storage-format` volume label; the name suffix already carries
  the format.
