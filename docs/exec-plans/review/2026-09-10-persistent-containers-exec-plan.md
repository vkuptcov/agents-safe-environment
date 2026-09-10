# Persistent Session Containers

Status: in review
Created: 2026-09-10
Design: [Safe environment](../../design-docs/agents-safe.md)
Scope:
- `internal/launcher/dockercli/` (create argv, `start`, inspection fields)
- `internal/launcher/` container lifecycle, host MCP reuse, fingerprint, `hostmcp/`
- `internal/launcher/launchplan/`, `internal/launcher/projectenv/`, `internal/cli/`
- `tests/smoke/`
- `docs/design-docs/agents-safe.md`, `docs/design-docs/go-session-manager.md`,
  `docs/design-docs/project-launcher-configuration.md`

## Objective

Let a project opt out of Docker's automatic `--rm` removal so long-lived state set up inside the session
container (nested Docker images, installed packages, anything outside tmpfs) survives idle shutdown, and make the
next launch restart and reuse that stopped container instead of failing on its occupied name. Removal stays the
default.

## Done Criteria

- `common.keep_container = true` or `--keep-container` creates the session container without `--rm`.
- Without the option, creation keeps `--rm` and the existing lifecycle is byte-for-byte unchanged.
- A launch that finds a stopped, owned, non-auto-remove container with a matching fingerprint restarts it, prints a
  stderr notice, and executes the command in it.
- A stopped container that fails ownership or fingerprint validation fails the launch with the existing mismatch
  error extended with a `docker rm` hint; `--force-exec` bypasses the fingerprint predicate as it does for running
  containers.
- A stopped auto-remove container is still awaited as Docker's asynchronous removal.
- When the launch forwards host MCP, the recorded generation directory is recreated and the relay sidecar is
  ensured before `docker start`, and the channel is ready before the command runs.
- The relay sidecar keeps `--rm`; the session manager and bootstrap are unchanged.
- Design docs and configuration docs describe the option, its restart path, and what still does not persist.
- `gofmt`, `make lint`, `make test`, `make check-docs` pass; `make test-smoke-go` proves a real restart on a Sysbox host.

## Current Baseline

Both the session container and the relay sidecar are created with `docker run --detach --rm`, so Docker removes the
session when the manager's idle shutdown exits. `acquireContainer` treats a stopped container as removal in
progress and waits up to 20s for the name to be released, then fails. The design docs state that stopped containers
are never restarted. The relay sidecar removes its generation directory after lease EOF, so a restarted session's
bind-mount source would be absent. Bootstrap already reconciles an existing user, removes stale manager runtime
state, and keeps the nested daemon's data root under the container's writable layer.

## Implementation Decisions

- Creation-only option, not a fingerprint input: the option only selects `--rm`. Reuse and restart are decided by
  the inspected `HostConfig.AutoRemove`, so a one-off `--keep-container` does not break later plain launches.
- Restart is driven by `AutoRemove == false`, regardless of the current launch's option value; the stderr notice
  makes that visible and names `docker rm` as the way to discard the container.
- Fingerprint and ownership predicates are reused unchanged for stopped containers; `--force-exec` applies too.
- Generation directory recreation is bounded to `<XDG_RUNTIME_DIR>/agents-safe/<project key>/`; a recorded path
  outside it fails closed.
- Sidecar image on restart is the stopped session's `inspection.Image`, as on running reuse.

## Phases

### Phase 1: Docker transport

Purpose: give the launcher the primitives a restart needs.
Status: done
Done when: `dockercli` can create without `--rm`, start a container by name, and report `AutoRemove`.

1. Add `CreateRequest.KeepContainer` and drop `--rm` from `BuildCreateArgs` when set; test both argv shapes.
2. Add `HostConfig.AutoRemove` to `ContainerInspection`; test parsing.
3. Add `Client.Start(ctx, name)`; test success and failure reporting.

### Phase 2: Option plumbing

Purpose: expose the option through config and CLI.
Status: done
Done when: config, override, and option resolve to the create request.

1. Add `CommonConfig.KeepContainer`, `Overrides.KeepContainer*`, `Options.KeepContainer`; resolve like `NoHostMCP`.
2. Add `--keep-container` to `internal/cli`; extend usage text.
3. Pass the option into `buildCreateRequest`.

### Phase 3: Restart path

Purpose: reuse a stopped persistent container.
Status: done
Done when: launcher unit tests cover restart, mismatch, auto-remove wait, and host MCP recreation.

1. In `waitForReusableOrReleased`, branch on `AutoRemove` for stopped owned containers; `acquireContainer` and
   `containerStoppedAfterExec` reach it through their existing paths.
2. Add `restartContainer`: validate fingerprint, adopt and recreate the channel, ensure the sidecar, `Start`,
   await readiness and channel, print notice and banner.
3. Add `hostmcp.Channel.Ensure` with the runtime-dir containment check.
4. Extend the mismatch error text with the `docker rm` hint.

### Phase 4: Documentation and smoke

Purpose: keep durable contracts and real-host proof in sync.
Status: done
Done when: docs describe the option and a smoke test restarts a kept container.

1. Update the three design docs and `README.md` usage where flags are listed.
2. Add a Sysbox smoke scenario: keep, idle stop, relaunch sees a file written in the first session.

## Validation Gates

- `go test ./internal/launcher/... ./internal/cli/...` passes.
- `make lint` and `make test` pass.
- `make check-docs` passes.
- `make test-smoke-go` passes on a Sysbox host, or the environment blocker is reported.

## Risks and Constraints

- Tmpfs masks and the container-local home on tmpfs (if any) are recreated on start; only the writable layer and
  nested Docker data root persist. The docs must say so.
- `XDG_RUNTIME_DIR` is cleared at logout; the launcher must recreate the generation directory, not assume it.
- Two concurrent launchers may both `docker start` the same container; `docker start` is idempotent.

## Out of Scope

- Pruning or listing persistent containers from the CLI.
- Persisting tmpfs-masked directories.

## Progress Notes

- 2026-09-10: plan created.
- 2026-09-10: phases 1-4 implemented test-first. `go test`, `make lint`, `make test`, and `make check-docs` pass.
  `TestSysboxKeptContainerRestarts` is written but not executed: the implementing host registers no `sysbox-runc`
  runtime and has no local `agents-safe-mvp:local` image, so `make test-smoke-go` must run on a Sysbox host before
  acceptance.
- 2026-09-10: implementation review F-001 (forced restart must rebuild the recorded relay) and F-002 (`.bashrc`
  overwritten on restart) fixed test-first; see the review report for responses.
- 2026-09-10: simplification pass. The `AutoRemove` decision lives only in `waitForReusableOrReleased` (one extra
  `docker inspect` on the restart path); `reuseHostMCP` and `prepareHostMCPRestart` share
  `adoptChannelAndEnsureSidecar`; `NewChannel` and `EnsureChannel` share `Channel.materialize`; `ParseLabel` reuses
  the discovery port check and the endpoint comparator. Gates re-run and pass.
