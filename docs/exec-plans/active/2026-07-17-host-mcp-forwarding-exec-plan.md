# Exec Plan: Host MCP Forwarding

- Status: active
- Created: 2026-07-17
- Design: [`docs/design-docs/host-mcp-forwarding.md`](../../design-docs/host-mcp-forwarding.md)
- Scope:
  - `internal/launcher/hostmcp/`, `internal/launcher/`, `internal/launcher/dockercli/`
  - `internal/container/`, `cmd/codex-safe-session/`
  - `internal/cli/`, `cmd/codex-safe/`, `cmd/agents-safe/`, `internal/testutil/clitest/`
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
- The cold-start bound is enforced, not assumed. Nothing between sidecar creation and the first lease is bounded
  today, so the design's inequality would be decorative: a slow `usermod` or `visudo` would let the sidecar give up
  while `serve` was still starting, and no finite initial-lease timeout could prevent it. This plan therefore
  creates two real deadlines and derives the bound from them:
  - Session-container create/start deadline: 20s, enforced by the launcher on that Docker call.
  - `serve` pre-lease deadline: 20s, covering account reconcile, filesystem prep, and first lease acquisition.
    Expiry fails bootstrap with that diagnostic, which the design already requires of a lease that cannot open.
  - Cold sidecar-create-to-first-lease bound: 40s, the sum of the two, and now a fact rather than a hope.
- Intervals, satisfying `max(40s, 2s) < 60s < 90s` with a 20s margin:
  - `serve` lease retry interval: 2s.
  - Sidecar initial-lease timeout: 60s.
  - Launcher readiness timeout: 90s.
- Sidecar creation happens after user-mount materialization, immediately before the session create.
  `materializeUserMounts` can block on an interactive Codex-home prompt, so a sidecar created before it would burn
  its initial-lease timeout waiting for a human. The design only requires the sidecar to precede the session
  container, which this satisfies.
- Name-release bound on recovery: reuse the existing `containerStateTimeout` of 20s, which the spike's measured
  30 ms release clears by three orders of magnitude. It precedes the readiness probe and is not inside the 90s
  readiness deadline.
- Readiness probing watches liveness, not just the socket. The launcher fails as soon as the session or sidecar
  stops or disappears, surfacing that container's diagnostic instead of waiting out 90s and blaming readiness.
- Preflight pulls an absent image. `Preflight` already runs `docker image inspect` and fails on a missing image, so
  a pull placed later in the MCP branch would be unreachable. The design's stated rationale for the pull — that
  `docker run` performs it implicitly today — is false about this code, verified by running the launcher against an
  absent tag; Phase 3 corrects that sentence in the design.
- Sidecar parent mount target `/run/codex-safe-mcp/`: the design names only the session container's
  `/run/codex-safe-host-mcp/`. A distinct target keeps the two roles' paths from being confused.
- Two packages the design's code map does not name, added because the container image builds from
  `cmd/codex-safe-session` plus a fixed list of internal directories and so cannot import launcher code:
  - `internal/mcpchannel/`: the socket names and one-byte control protocol shared by all three processes, with no
    dependencies, so the two ends of one private protocol cannot drift.
  - `internal/relay/`: the relay itself, leaving `cmd/codex-safe-session/` the `relay` mode the design names, exactly
    as `serve` there delegates to `internal/container/`.
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
Status: done
Done when: discovery selects, deduplicates, expands, and rejects endpoints exactly as the design's Test Plan states.

1. Add `github.com/BurntSushi/toml` to `go.mod`, recording the owner approval already in the design.
2. Create `internal/launcher/hostmcp/` with the endpoint type, its canonical sorted form, and the socket index.
3. Implement `config.toml` decode of `url` and tri-state `enabled` per `mcp_servers.<name>` entry.
4. Implement loopback selection, scheme default ports, deduplication, and listener expansion.
5. Implement collision rejection naming both servers, both endpoints, and the contested address.
6. Implement the fail-closed launch errors: unreadable file, TOML parse error, unparsable URL, invalid port.
7. Add unit tests for every Discovery test in the design's Test Plan.

### Phase 2: Typed Create Request and Container Lifecycle
Purpose: Let one typed client express both containers and perform the sidecar lifecycle Phase 6 needs.
Status: done
Done when: `BuildCreateArgs` can emit both create requests, no session container can lose its explicit runtime, and
the client can stop a sidecar and await its removal.

1. Add `Command`, `User`, `NetworkMode`, `ReadOnlyRootfs`, `CapDrop`, `SecurityOpt` to `CreateRequest`.
2. Relax `BuildCreateArgs` to accept an empty `Runtime` and `WorkingDir`, emitting the flags only when set.
3. Append `Command` after the image, preserving argument order.
4. Keep the explicit-runtime invariant at the session request builder and cover it with a test.
5. Add a typed `Stop`, which the client lacks and the race loser's sidecar needs. The bounded wait for the
   asynchronous `--rm` name release stays launcher policy built on `Inspect`, matching the existing
   `waitForReusableOrReleased`, so no polling enters the transport.
6. Add unit tests for argv order, the sidecar's full flag set, the session request's unchanged output, and `Stop`
   against a fake runner, including an already-removed container.

### Phase 3: Image Identity and Entrypoint Split
Purpose: Make an image reference resolvable to one immutable ID and let the sidecar select `relay` through the image.
Status: done
Done when: an absent reference is pulled and resolves to an immutable ID, and `tini` still owns PID 1 with an
unchanged default `serve` command. Creating both containers from that ID is Phase 6's outcome, not this phase's.

1. Split `container/Dockerfile` into `ENTRYPOINT [tini, --, codex-safe-session]` and `CMD ["serve"]`.
2. Make `Preflight` pull an absent image instead of failing, since its existing `docker image inspect` would
   otherwise reject a fresh host before any later pull could run.
3. Add image-ID resolution to `dockercli`, returning the immutable content ID of a local reference.
4. Add the inspected container image ID to `ContainerInspection`.
5. Correct the design's `Image identity` rationale, which claims `docker run` performs the pull implicitly today;
   the launcher actually fails preflight on an absent reference.
6. Add unit tests for pull-then-resolve, resolve-only when present, and the inspected `.Image` on reuse.
7. Run `make docker-build` and confirm the session container's effective process is unchanged.

### Phase 4: Relay Sidecar
Purpose: Serve the channel sockets and reach host loopback from a confined container.
Status: done
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
Status: done
Done when: `serve` binds one listener per concrete address and holds the lease for the session's life.

1. Implement the forwarders in `internal/container/`: listen, dial the endpoint socket, copy with half-close.
2. Apply the `localhost` rule: at least one leg must bind; an unavailable address family is logged and skipped.
3. Fail `serve` when an explicitly configured listener cannot bind.
4. Acquire the initial lease synchronously under the 20s pre-lease deadline, retrying every 2s inside it so a
   sidecar that has not yet bound `control.sock` is tolerated. Expiry fails bootstrap with that diagnostic.
5. Enter the background 2s retry loop only after an established lease is lost, never for the initial acquisition.
6. Wire both into `Serve` after `prepareContainerUserFilesystem` and before `startDockerDaemon`, with the pre-lease
   deadline covering account reconcile, filesystem prep, and the first lease together.
7. Parse `CODEX_SAFE_HOST_MCP`, treating an absent or empty value as no lease and no listener.
8. Add unit tests for the forwarder and serve-lease tests in the design's Test Plan, including a bootstrap that
   exceeds the pre-lease deadline and one whose sidecar never binds.

### Phase 6: Launcher Wiring
Purpose: Create the channel, the sidecar, and the session in the design's order, and print the boundary widening.
Status: done
Done when: a cold launch forwards a loopback endpoint and an empty set behaves exactly as today.

1. Create the `0700` generation directory under `XDG_RUNTIME_DIR` before either container, validating ownership
   and the 108-byte socket-path budget.
2. Create the sidecar after `materializeUserMounts` and immediately before the session create, so no interactive
   prompt can sit inside its initial-lease window.
3. Build the sidecar create request with its deterministic name, labels, single mount, and `relay` command, from
   the image ID resolved once for this launch.
4. Add `CODEX_SAFE_HOST_MCP`, the generation mount, and both host-MCP labels to the session create request, and
   bound the session create/start call at 20s.
5. Probe `control.sock` until ready under the 90s readiness timeout while watching both containers, failing at once
   with their diagnostic if either stops or disappears; then print the forwarded endpoints.
6. Implement reuse: compare `codex-safe.host-mcp`, adopt `codex-safe.host-mcp-channel`, remove the candidate.
7. Implement recovery: adopt a running sidecar; for a non-running one await name release within 20s and retry
   create exactly once, from the session's inspected image ID.
8. Implement loser cleanup: stop and await only this attempt's sidecar, remove only its generation.
9. Add unit tests for every Launcher contract test in the design's Test Plan, plus the three sidecar-name branches
   and a session that dies during readiness probing.

### Phase 7: Command Interface
Purpose: Give the user the one control the design specifies, consistently across both binaries.
Status: to be done
Done when: `--no-host-mcp` skips discovery entirely, both binaries document it, and its diagnostic names the
narrowing case.

1. Add `--no-host-mcp` to `internal/cli/` and choose the launch-options shape that carries it to the launcher.
2. Extend the `Launcher` interface and the `clitest` recording double for that shape.
3. Update the hard-coded usage text in `cmd/codex-safe/` and `cmd/agents-safe/`, which is not generated from the
   flag set and would otherwise advertise an incomplete interface.
4. Prove the flag performs no `config.toml` read.
5. Report the narrowing diagnostic when the flag meets a live forwarding session.
6. Add unit tests for the flag, both usage strings, and both diagnostics.

### Phase 8: Real-Host Proof and Documentation
Purpose: Prove the feature on real Sysbox and leave the docs true.
Status: to be done
Done when: the design's Sysbox integration tests and security gate pass, and the docs match the implementation.

1. Add the Sysbox integration tests from the design's Test Plan to `tests/smoke/`.
2. Add the security-review gate assertions for both containers.
3. Record the real cold start against the 40s bound as evidence, not as the proof that the bound holds; the
   deterministic deadline tests are that proof.
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
- `rg -n 'no-host-mcp' internal/cli internal/launcher cmd/codex-safe cmd/agents-safe` shows the flag reaches
  discovery and both usage strings.
- A test asserts `retry(2s) < initialLease(60s) < readiness(90s)` and `coldStart(40s) < initialLease(60s)`, where
  40s is the sum of the two enforced deadlines rather than an assumed allowance.
- Deterministic tests, not a performance measurement, prove each deadline fires: a pre-lease bootstrap held past
  20s fails `serve` with its own diagnostic, and a session create held past 20s fails the launch.
- A test proves the launcher abandons readiness the moment the session or sidecar stops, reporting that
  container's diagnostic rather than a readiness timeout.
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
- The design's inequality only means something while both cold-start components stay enforced. Removing either
  deadline, or moving work in front of the lease that neither covers, silently returns the sidecar to giving up on
  a session that was still starting.
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
