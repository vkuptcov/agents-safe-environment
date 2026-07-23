# Venv tmpfs exec permission

Status: in review
Created: 2026-07-23
Design: [Safe environment](../../design-docs/agents-safe.md)
Scope:
- `internal/container/tmpfs.go`
- `internal/launcher/docker_requests.go`
- `internal/launcher/dockercli/request.go`, `internal/launcher/dockercli/create.go`
- `internal/launcher/launch_fingerprint.go`
- `docs/design-docs/agents-safe.md`

## Objective

Make Python virtual-environment tmpfs masks executable so native extension modules installed into a
masked `.venv` can be loaded, while generic scratch tmpfs mounts keep `noexec`.

## Done Criteria

- A `.venv` masked by a session tmpfs can import a package with a compiled extension module
  (for example `pydantic-core`) without `ImportError`.
- Explicit `common.tmpfs_mounts` targets that are not virtual environments stay non-executable.
- Sessions created before this change are recreated rather than silently reused with `noexec` masks.

## Current Baseline

Both mount paths mark every session tmpfs `noexec`:

- Docker receives `--tmpfs <target>:mode=<mode>`, and Docker's `--tmpfs` default option set is
  `rw,nosuid,nodev,noexec`.
- Privileged bootstrap remounts with the fixed constant `nosuid,nodev,noexec`
  (`internal/container/tmpfs.go`).

`uv sync` therefore populates `.venv` correctly, but `dlopen` of any `.so` under it fails because the
loader's `mmap(PROT_EXEC)` is refused on a `noexec` filesystem. The same bytes execute from the
worktree bind and from the container's writable layer, which is why the failure looks file-specific.

Confirmed on a live session:

```
tmpfs /home/<user>/<project>/.venv tmpfs rw,nosuid,nodev,noexec,relatime,uid=100000,...
```

## Implementation Decisions

- Derive the exec bit from the existing `Owned` flag rather than adding new configuration. `Owned`
  already marks exactly the proactive, discovered, and upgraded virtual-environment masks, and it
  already rides the bootstrap wire.
- Keep `nosuid,nodev` on every session tmpfs; only `noexec` is relaxed, and only for venv masks.
- Bump `launchConfigSchemaVersion` so live containers created with `noexec` masks are recreated
  instead of reused. The exec bit itself stays out of the fingerprint payload because it is derived,
  not user-configurable.

## Phases

### Phase 1 — Executable venv masks on both mount paths

Purpose: remove the `noexec` restriction from virtual-environment masks wherever the mount is made.
Status: done
Done when: a masked `.venv` mounts with `exec` both from Docker's creation-time tmpfs and from the
privileged bootstrap remount, and non-venv scratch mounts remain `noexec`.

- Add an exec selector to `dockercli.TmpfsMount` and emit an explicit option list from
  `tmpfsMountArg` instead of relying on Docker's defaults.
- Set that selector from `launchplan.TmpfsMount.Owned` in `dockerTmpfsMounts`.
- Carry the same distinction into `mountContainerTmpfs` so the bootstrap remount is per-mount.
- Bump `launchConfigSchemaVersion`.

### Phase 2 — Tests and design-doc update

Purpose: pin the contract so a future change cannot silently restore `noexec` on venv masks.
Status: done
Done when: unit tests assert the emitted Docker argument and the bootstrap mount options for both
mount classes, and `docs/design-docs/agents-safe.md` states the executability rule.

## Validation Gates

- `gofmt -l` over changed Go files reports nothing.
- `go test ./internal/container/... ./internal/launcher/...` passes.
- `make check-docs` passes.
- Manual: in a recreated session, `python -c "import pydantic_core"` succeeds from a masked `.venv`,
  and `findmnt -no OPTIONS <venv>` contains no `noexec`.

## Risks and Constraints

- Relaxing `noexec` on venv masks is a deliberate confinement reduction. It is required for the mount
  to serve its purpose; `nosuid` and `nodev` still hold, and the mask is still session-local.
- The schema-version bump recreates every existing session on next launch.

## Out of Scope

- Per-mount executability configuration in `common.tmpfs_mounts`.
- Any change to `use_host_python_venv` behavior.

## Progress Notes

- Root cause confirmed on a live session before any change: the `.venv` mask carried
  `rw,nosuid,nodev,noexec`. The same `.so` bytes executed from the worktree bind and from the
  container's writable layer, which is what made the failure look file-specific rather than
  mount-specific.
- Verified independently that Docker accepts `exec` in a `--tmpfs` option list and drops `noexec`
  from the resulting mount, so the launcher-side change does not depend on the bootstrap remount
  alone.
- `go test ./internal/...`, `gofmt -l internal/`, and `make check-docs` pass.
- The manual gate — importing a compiled extension module from a masked `.venv` — is still pending.
  It requires recreating a session, and the only live managed container had work running.
