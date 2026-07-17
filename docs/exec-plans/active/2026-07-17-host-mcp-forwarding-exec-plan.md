# Exec Plan: Host MCP Forwarding

- Status: active
- Created: 2026-07-17
- Design: [`docs/design-docs/host-mcp-forwarding.md`](../../design-docs/host-mcp-forwarding.md)
- Scope:
  - `internal/launcher/hostmcp/`, `internal/launcher/`, `internal/launcher/dockercli/`
  - `internal/container/`, `cmd/codex-safe-session/`, `internal/cli/`
  - `container/Dockerfile`
  - `tests/smoke/`
  - `go.mod`, `go.sum` (one owner-approved dependency)
  - design, testing, and dependency docs listed under Phase 8

## Objective

Make an enabled loopback MCP server in the base `mcp_servers` table answer inside the session container at the
address its URL names, without rewriting the user's `config.toml`, binding a host TCP port, or letting the agent
reach any host port its configuration does not name. The end state is the contract in the design doc, implemented
and covered by the design's Test Plan.

## Done Criteria

- A loopback MCP server configured in the resolved Codex home answers inside the container at the same address and
  port; a public URL keeps working with no forwarder involved.
- An empty endpoint set adds no environment variable, mount, relay, listener, or banner line.
- `--no-host-mcp` disables discovery, both forwarders, the relay, and the mount.
- A running container is reused only when its `codex-safe.host-mcp` label equals the current resolution.
- The relay sidecar is created with host networking only, the recreated host identity, `--read-only`,
  `--cap-drop=ALL`, `no-new-privileges`, `--rm`, the Docker default runtime, one mount, and no Docker socket.
- Every session container is still created with an explicit `sysbox-runc` runtime.
- The three configured intervals satisfy the design's ordering rule with an explicit margin.
- `make test` and `make check-docs` pass; `make test-smoke-go` passes on a Sysbox host.
- Only `github.com/BurntSushi/toml` is added to `go.mod`.

## Current Baseline

The design is accepted and its Linux/Sysbox spike passed on 2026-07-17
([verdict](../review/2026-07-17-host-mcp-sidecar-spike-exec-plan.md)), so this plan is authorized. No feature code
exists: there is no `internal/launcher/hostmcp/`, no `relay` mode, no `--no-host-mcp`, and no TOML dependency.

The pieces this plan must change:

- `dockercli.CreateRequest` has only `Image`, `Name`, `Runtime`, `WorkingDir`, `Labels`, `Environment`, `Mounts`.
  It has no `Command`; `Command` exists only on `ExecRequest`.
- `BuildCreateArgs` emits `docker run --detach --rm --runtime=<runtime>` and rejects an empty `Runtime` or
  `WorkingDir`. The sidecar needs the default runtime, no working directory, and an explicit command.
- `dockercli.Client` has `Create`, `Inspect`, `Exec`, and `Preflight`. It cannot pull an image or report a
  container's image ID; `ContainerInspection` exposes only `Id`, `Config.Labels`, and `State`.
- `container/Dockerfile` ends with a fixed `ENTRYPOINT [... "codex-safe-session", "serve"]` and no `CMD`.
- `Supervisor.Serve` runs `reconcileContainerAccount`, `prepareContainerUserFilesystem`, `startDockerDaemon`, then
  the session manager. There is no lease and no listener.
- `cli.Run` parses only `--project` and `--image`.

## Implementation Decisions

- Lease before `dockerd`: `serve` opens the lease and binds the container listeners after
  `prepareContainerUserFilesystem` and before `startDockerDaemon`. The design allows anywhere "after account
  bootstrap and before the manager listener"; placing it before `dockerd` keeps that daemon's 60-second ready
  timeout out of the cold-start budget, which would otherwise force every downstream timeout above it.
- Neither the lease nor the listeners need `dockerd`: the lease dials a Unix socket and the listeners bind the
  container's own loopback, so this placement costs nothing.
- Intervals, derived below and satisfying `max(30s, 2s) < 60s < 90s`:
  - `serve` lease retry interval: 2s.
  - Cold sidecar-create-to-first-lease budget: 30s.
  - Sidecar initial-lease timeout: 60s.
  - Launcher readiness timeout: 90s.
- Budget rationale: no hard bound exists today on container create, account reconcile, or filesystem prep, so the
  30s budget is an explicit allowance, not a composition of existing bounds. The spike measured 314-386 ms for
  create-to-lease with a trivial bootstrap; real bootstrap adds account and filesystem work. 30s is roughly a
  10-70x margin, and a smoke test asserts the real cold start stays well inside it.
- Sidecar parent mount target `/run/codex-safe-mcp/`: the design names only the session container's
  `/run/codex-safe-host-mcp/`. A distinct target keeps the two roles' paths from being confused.
- Generation identifier: 16 hex characters from `crypto/rand`. With a 24-character project key this keeps the
  deepest host socket path near 80 bytes, inside the 108-byte `sockaddr_un` limit, which preflight still validates.
- Control protocol bytes: `L` lease, `P` probe, answered by `R` ready or `N` not ready. The design fixes the
  one-byte role framing but not the values; the spike used these.
- `Runtime` becomes optional in `BuildCreateArgs` and `WorkingDir` becomes optional. The explicit-runtime invariant
  is enforced where the session request is built, not in the shared argv builder, so the sidecar is the only caller
  that can omit a runtime.
- Discovery reads the resolved Codex home from the existing user-mount resolution, so `--no-host-mcp` and an
  absent Codex home both reduce to an empty endpoint set with no new resolution path.

## Phases

### Phase 1: Endpoint Discovery
Purpose: Turn the base `mcp_servers` table into a validated endpoint set with no Docker involved.
Status: to be done
Done when: discovery selects, deduplicates, expands, and rejects endpoints exactly as the design's Test Plan states.

1. Add `github.com/BurntSushi/toml` to `go.mod`, recording the owner approval already in the design.
2. Create `internal/launcher/hostmcp/` with the endpoint type, its canonical sorted form, and the socket index.
3. Implement `config.toml` decode of `url` and tri-state `enabled` per `mcp_servers.<name>` entry.
4. Implement loopback selection, scheme default ports, deduplication, and listener expansion.
5. Implement collision rejection naming both servers, both endpoints, and the contested address.
6. Implement the fail-closed launch errors: unreadable file, TOML parse error, unparsable URL, invalid port.
7. Add unit tests for every Discovery test in the design's Test Plan.

### Phase 2: Typed Create Request
Purpose: Let one typed request express both the session container and the confined sidecar.
Status: to be done
Done when: `BuildCreateArgs` can emit both create requests and no session container can lose its explicit runtime.

1. Add `Command`, `User`, `NetworkMode`, `ReadOnlyRootfs`, `CapDrop`, `SecurityOpt` to `CreateRequest`.
2. Relax `BuildCreateArgs` to accept an empty `Runtime` and `WorkingDir`, emitting the flags only when set.
3. Append `Command` after the image, preserving argument order.
4. Keep the explicit-runtime invariant at the session request builder and cover it with a test.
5. Add unit tests for argv order, the sidecar's full flag set, and the session request's unchanged output.

### Phase 3: Image Identity and Entrypoint Split
Purpose: Give both containers one immutable image ID and let the sidecar select `relay` through the image.
Status: to be done
Done when: a mutable tag resolves once to an ID both containers are created from, and `tini` still owns PID 1.

1. Split `container/Dockerfile` into `ENTRYPOINT [tini, --, codex-safe-session]` and `CMD ["serve"]`.
2. Add `Client.Pull` and image-ID resolution to `dockercli`, pulling only when the reference is absent locally.
3. Add the inspected container image ID to `ContainerInspection`.
4. Add unit tests for pull-then-resolve, resolve-only when present, and the inspected `.Image` on reuse.
5. Run `make docker-build` and confirm the session container's effective process is unchanged.

### Phase 4: Relay Sidecar
Purpose: Serve the channel sockets and reach host loopback from a confined container.
Status: to be done
Done when: `codex-safe-session relay` binds, leases, serves bytes, and cleans up exactly as the design states.

1. Add the `relay` subcommand to `cmd/codex-safe-session/`, parsing its endpoint set and generation.
2. Implement stale-socket removal, all-or-nothing endpoint binding, then `control.sock` last.
3. Implement the control protocol: one lease, refused second lease, probe answering ready only once leased.
4. Implement the data path: accept, dial the configured host as written, copy both ways with half-close.
5. Implement lifetime: established-lease EOF removes the generation and exits; signal, internal failure, or
   initial-lease timeout removes only socket entries and preserves the inode.
6. Add unit tests for every Relay sidecar test in the design's Test Plan.

### Phase 5: Container Forwarders and Lease
Purpose: Make the unmodified URL resolve inside the container and tie the sidecar's lifetime to `serve`.
Status: to be done
Done when: `serve` binds one listener per concrete address and holds the lease for the session's life.

1. Implement the forwarders in `internal/container/`: listen, dial the endpoint socket, copy with half-close.
2. Apply the `localhost` rule: at least one leg must bind; an unavailable address family is logged and skipped.
3. Fail `serve` when an explicitly configured listener cannot bind.
4. Implement the lease client with the 2s retry interval, retrying in the background after loss.
5. Wire both into `Serve` after `prepareContainerUserFilesystem` and before `startDockerDaemon`.
6. Parse `CODEX_SAFE_HOST_MCP`, treating an absent or empty value as no lease and no listener.
7. Add unit tests for the forwarder and serve-lease tests in the design's Test Plan.

### Phase 6: Launcher Wiring
Purpose: Create the channel, the sidecar, and the session in the design's order, and print the boundary widening.
Status: to be done
Done when: a cold launch forwards a loopback endpoint and an empty set behaves exactly as today.

1. Create the `0700` generation directory under `XDG_RUNTIME_DIR` before either container, validating ownership
   and the 108-byte socket-path budget.
2. Build the sidecar create request with its deterministic name, labels, single mount, and `relay` command.
3. Add `CODEX_SAFE_HOST_MCP`, the generation mount, and both host-MCP labels to the session create request.
4. Probe `control.sock` until ready with the 90s readiness timeout, then print the forwarded endpoints.
5. Implement reuse: compare `codex-safe.host-mcp`, adopt `codex-safe.host-mcp-channel`, remove the candidate.
6. Implement recovery: recreate a missing sidecar from the session's inspected image ID, awaiting name release
   boundedly and retrying create exactly once.
7. Implement loser cleanup: stop and await only this attempt's sidecar, remove only its generation.
8. Add unit tests for every Launcher contract test in the design's Test Plan.

### Phase 7: Command Interface
Purpose: Give the user the one control the design specifies.
Status: to be done
Done when: `--no-host-mcp` skips discovery entirely and its diagnostic names the narrowing case.

1. Add `--no-host-mcp` to `internal/cli/` and thread it to the launcher.
2. Prove the flag performs no `config.toml` read.
3. Report the narrowing diagnostic when the flag meets a live forwarding session.
4. Add unit tests for the flag and both diagnostics.

### Phase 8: Real-Host Proof and Documentation
Purpose: Prove the feature on real Sysbox and leave the docs true.
Status: to be done
Done when: the design's Sysbox integration tests and security gate pass, and the docs match the implementation.

1. Add the Sysbox integration tests from the design's Test Plan to `tests/smoke/`.
2. Add the security-review gate assertions for both containers.
3. Assert the real cold start stays inside the 30s budget.
4. Update `docs/dependencies.md` with the TOML approval record.
5. Set the design's `Status` and update `ARCHITECTURE.md` if module ownership changed.
6. Run `make test`, `make test-smoke-go`, and `make check-docs`.

## Validation Gates

- `go test ./internal/launcher/hostmcp` passes and covers every Discovery test in the design's Test Plan.
- `go test ./internal/launcher/dockercli` proves the sidecar argv carries `--network=host`, `--user`, `--read-only`,
  `--cap-drop=ALL`, `--security-opt=no-new-privileges`, `--rm`, no `--runtime`, and the command after the image.
- `go test ./internal/launcher` proves a session create request always carries `--runtime=sysbox-runc`.
- `docker image inspect codex-safe-mvp:local --format '{{json .Config.Cmd}}'` reports `["serve"]` and the entrypoint
  no longer contains `serve`.
- `go test ./internal/container ./cmd/codex-safe-session` covers the relay, forwarder, and serve-lease tests.
- `rg -n 'no-host-mcp' internal/cli internal/launcher` shows the flag reaches discovery.
- A test asserts `retry(2s) < initialLease(60s) < readiness(90s)` and `coldStartBudget(30s) < initialLease(60s)`.
- `make test-smoke-go` passes on a Sysbox host, including a real loopback MCP endpoint answered inside the container
  and the same sentinel unreachable under `--no-host-mcp`.
- `git diff -- go.mod go.sum` adds only `github.com/BurntSushi/toml` and no transitive dependency.
- `make test`, `make check-docs`, and `git diff --check` pass.

## Risks and Constraints

- The sidecar shares the host network namespace. Every other confinement flag is load-bearing; losing one is a
  security regression, not a cosmetic change.
- Relaxing `Runtime` in the shared argv builder removes the mechanism that currently makes a session container
  falling off `sysbox-runc` impossible. The replacement invariant must be enforced and tested where the session
  request is built.
- The spike's numbers are throwaway and measured on one host; the intervals above are derived with explicit margin
  and must not be tuned down to match a measurement.
- `--rm` removal is asynchronous, so a same-name sidecar create can conflict legitimately. The spike observed this
  at 30 ms; the bounded wait and single retry are required, not optional.
- Discovery runs in the launcher's fail-closed preflight over a user-authored file. A parser panic or a permissive
  read would be a launch-blocking bug on a path users cannot avoid.
- Forwarding widens the sandbox by design. The endpoint set must never exceed what discovery selected from the base
  table.

## Out of Scope

- Discovery from a profile, a trusted project-level `.codex` configuration, or `-c` overrides.
- MCP or HTTP protocol parsing, authentication, authorization, or auditing of MCP requests.
- Forwarding a Unix-socket MCP server or making a stdio server's host executable available in the container.
- Picking up a `config.toml` change during a live session.
- Automatic collection of crash-residue generation directories.
- Docker Desktop, macOS, and Windows.

## Progress Notes

- Add dated execution notes before moving this plan to `review/`.
