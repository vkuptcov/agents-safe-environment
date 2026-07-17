# Exec Plan: Host MCP Relay Sidecar Linux/Sysbox Spike

- Status: active
- Created: 2026-07-17
- Design: [`docs/design-docs/host-mcp-forwarding.md`](../../design-docs/host-mcp-forwarding.md)
- Scope:
  - temporary spike code under `tests/smoke/`
  - `docs/design-docs/host-mcp-forwarding.md`
  - `docs/reviews/feature-review/2026-07-17-host-mcp-forwarding-design-review.md`
  - this plan and `docs/exec-plans/index.md`

## Objective

Produce a decisive real-host verdict for the seven Linux/Sysbox assumptions that gate the host-MCP forwarding
design. Use a disposable Go/Moby harness and throwaway image to test the proposed sidecar topology without starting
feature implementation or adding production dependencies.

## Done Criteria

- The tested Linux, Docker, Sysbox, kernel, UID/GID, runtime-directory, and image identities are recorded.
- Each of the design's seven spike requirements has a PASS or FAIL result backed by an observed value or lifecycle
  event.
- Container inspection proves the relay has host networking, a non-root user, a read-only root filesystem, no
  capabilities, `no-new-privileges`, one runtime-parent mount, and no Docker socket.
- The session reaches a host-loopback sentinel through the sidecar-created Unix socket with the required ownership
  and mode.
- Launcher exit, session kill, sidecar restart, immutable-image recovery, and generation cleanup have decisive
  results.
- The design and this plan record one overall verdict: PASS permits the feature execution plan; FAIL returns to the
  design and does not permit that plan.
- All spike containers, images, runtime directories, and temporary source files are removed after evidence capture.
- Application and smoke module dependency files are unchanged.

## Current Baseline

The forwarding feature is not implemented. `codex-safe-session` has no relay mode, and the current image still starts
the existing session manager directly. The accepted design therefore requires an isolated proof before its feature
execution plan can be written.

The smoke module already has the Moby client, Testify, real-Sysbox fixtures, deterministic cleanup, bounded polling,
launcher subprocess diagnostics, and concurrency barriers needed for host orchestration. It does not yet contain a
host-MCP helper or sidecar scenario.

The spike must run on Linux with a reachable host Docker Engine, `sysbox-runc`, an owned `XDG_RUNTIME_DIR`, and the
local `codex-safe-mvp:local` image. A skip, Docker permission failure, missing runtime, or registry failure is an
environment blocker, not a PASS or a product FAIL.

## Implementation Decisions

- Harness ownership: use a temporary Go test in the existing `tests/smoke` module and its current Moby dependency.
- Helper: add a temporary standard-library-only binary with relay, lease-client, socket-client, and launcher-helper
  modes; do not change either Go module's dependency files.
- Image fidelity: build a uniquely tagged throwaway image from `codex-safe-mvp:local` with the helper installed and a
  spike-only Tini-plus-helper entrypoint. Supply no default command, so each spike container selects its helper mode
  through its Docker command without changing the production Dockerfile.
- Session fidelity: create the session directly with `sysbox-runc`, the generation-only mount, and the numeric host
  UID/GID. The spike tests namespace, ID-mapping, socket, and lifecycle assumptions, not launcher implementation.
- Sentinel: own a host Go echo server bound to `127.0.0.1:0` and use a random nonce as the byte-path assertion.
- Isolation: label every resource with `codex-safe.host-mcp-spike=<run-id>` and register cleanup before it starts.
- Synchronization: use channels, sockets, container events, and bounded polling; never use an unverified sleep as a
  correctness barrier.
- Timing evidence: measure elapsed durations with the host orchestrator's monotonic clock. Give each awaited lifecycle
  transition a 60-second spike-only watchdog so a hung experiment terminates; these watchdogs are failure bounds, not
  proposed production timeout values.
- Evidence: keep raw logs temporary; record environment facts, seven result rows, image IDs, inode values, timings,
  cleanup results, and the final verdict in `Progress Notes` and the existing design review.
- Disposal: remove the temporary test/helper sources and throwaway image after evidence is recorded. No spike code
  becomes production or permanent smoke coverage.

## Phases

### Phase 1: Environment Preflight and Disposable Harness
Purpose: Establish a qualified host and a self-cleaning prototype without changing production code.
Status: to be done
Done when: the helper image is runnable, every resource has a unique label, and preflight evidence proves the host can
execute a meaningful Sysbox spike.

1. Record kernel, Docker server, registered runtimes, Sysbox version, host UID/GID, `XDG_RUNTIME_DIR` mode/owner, and
   the base image ID.
2. Fail preflight unless the host is Linux, Docker is reachable, `sysbox-runc` is registered, and the runtime
   directory is owned by the invoking user with no group/other access.
3. Create temporary `tests/smoke/host_mcp_sidecar_spike_test.go` orchestration and
   `tests/smoke/cmd/host-mcp-sidecar-spike/main.go` helper files.
4. Build the helper with `CGO_ENABLED=0`; generate a temporary Dockerfile from `codex-safe-mvp:local` that installs it
   at `/usr/local/bin/host-mcp-sidecar-spike`, sets
   `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/host-mcp-sidecar-spike"]`, supplies no `CMD`, and adds the
   unique spike label. Each spike container selects its helper mode through its Docker command.
5. Register `t.Cleanup` for containers, image tags, temporary build state, listeners, and runtime directories before
   starting any Docker resource.
6. Compile and vet the smoke module with the opt-in spike disabled, proving the disposable harness is well-formed.

### Phase 2: Host Reachability and Sysbox Socket Ownership
Purpose: Prove the network and ID-mapping properties on which the two-hop channel depends.
Status: to be done
Done when: requirements 1 and 2 have observed results and the inspected sidecar matches its security contract.

1. Start a host echo server on `127.0.0.1:0` and retain a random request/response nonce.
2. Create a `0700` project/generation path under `XDG_RUNTIME_DIR` and start the sidecar as host UID/GID with host
   networking, read-only rootfs, all capabilities dropped, `no-new-privileges`, and only the runtime-parent mount.
3. Inspect the sidecar and assert the exact namespace, user, capability, rootfs, runtime, mount, label, and no-Docker-
   socket contract.
4. Make the sidecar dial the host sentinel and assert the nonce round-trip while the sentinel remains loopback-only.
5. Have the sidecar create `e0.sock` with mode `0600`; assert its host owner, group, type, and mode.
6. Start a non-privileged `sysbox-runc` session with only the generation mounted at
   `/run/codex-safe-host-mcp/`, connect as the numeric host user, and assert the same nonce through both hops.
7. Record requirement 1 and 2 results, the cold sidecar-create-to-first-successful-lease duration, and the relevant
   inspect and `stat` observations.

### Phase 3: Creation Arbitration and Launcher-Independent Lifetime
Purpose: Prove candidate cleanup and lifetime behavior before destructive shutdown scenarios.
Status: to be done
Done when: requirements 3 and 4 show one settled session/sidecar pair that survives its creating launcher.

1. Start two child launcher-helper processes against one deterministic session name, synchronized before create.
2. Give each attempt a distinct generation and candidate sidecar, then let both race for the session container name.
3. Require the loser to stop and await only its own sidecar, remove only its own generation, and adopt no resources
   before the winner is inspectably running.
4. Assert that after both helpers return there is one session container, one sidecar, and only the winning generation.
5. Run creation from a child launcher-helper that exits after handoff; retain an active session lease and data probe.
6. After the child exits, assert the session, sidecar, lease, and host-loopback nonce round-trip remain alive.
7. Record requirement 3 and 4 results, container IDs, generation names, and cleanup observations.

### Phase 4: Lease Closure, Sidecar Recovery, and Generation Isolation
Purpose: Prove destructive lifetime transitions and recovery without changing the live session mount.
Status: to be done
Done when: requirements 5 through 7 have decisive cleanup, recovery, inode, image-ID, and sibling-isolation evidence.

1. Kill the session container and assert lease EOF makes the sidecar exit, Docker removes it through `--rm`, and only
   its generation directory disappears without sidecar Docker access. Measure kill-to-lease-EOF and
   lease-EOF-to-sidecar-exit separately.
2. Start a fresh pair and record the session ID and immutable image ID, sidecar ID and deterministic name, and
   generation device/inode. Subscribe to Docker `die` and `destroy` events filtered by the old sidecar ID. Move only a
   run-specific mutable tag to a different image and verify that the tag's resolved image ID changed.
3. Stop the sidecar gracefully. On its `die` event, record a host-monotonic timestamp and immediately attempt to create,
   but not start, the replacement under the same name from the saved session image ID. If create succeeds, record the
   name-creatable timestamp. If and only if Docker reports a name conflict, await the old sidecar's `destroy` event
   under the 60-second watchdog and retry create exactly once; any other error or a second conflict fails the
   requirement. Record the first-create outcome and the monotonic die-to-name-creatable duration.
4. Before starting the replacement, assert that the old socket entries are gone and the generation device/inode is
   unchanged. Start it and assert the session ID, session image ID, generation device/inode, and replacement sidecar
   image ID match the saved originals while the mutable tag resolves to the different image.
5. Use the 60-second spike-only watchdog as the replacement's initial-lease failure bound. Measure the helper's
   observed lease retry gap, replacement-create-to-start, replacement-start-to-first-successful-lease,
   replacement-create-to-first-successful-lease, and lease-to-restored-data-path durations. Assert the live session
   reconnects and serves the nonce without replacement before the watchdog expires.
6. Delay an old sidecar's lease-EOF cleanup, create a newer sibling generation, release the old cleanup, and assert
   only the old directory disappears while the newer channel still serves traffic.
7. Record requirements 5 through 7, before/after inode values, image IDs, first-create outcome, the watchdog value,
   die-to-name-creatable time, and every raw closure, cold-start, retry, recovery, and restored-data-path duration.
   Require the feature execution plan to derive name-release wait, retry, initial-lease, and readiness timeout values
   from these measurements, the bounded bootstrap components, and an explicit safety margin while preserving the
   design's timeout inequality and single-retry rule.

### Phase 5: Verdict, Cleanup, and Design Handoff
Purpose: Convert the throwaway experiment into a durable design decision with zero runtime residue.
Status: to be done
Done when: the design records a PASS or FAIL verdict, temporary assets are gone, and the plan is ready for review.

1. Add a seven-row evidence table to `Progress Notes` and the existing design review, including environment facts and
   one concise observation per requirement.
2. On PASS, replace the design's open spike gate with the dated tested environment and authorize creation of the
   feature execution plan.
3. On FAIL, identify the falsified assumption, update the design before any feature plan, and record whether the
   detached host relay fallback requires a new design revision.
4. Remove every spike container, image/tag, temporary runtime directory, listener, log, and generated build context.
5. Delete the temporary spike test and helper source after their output has been captured.
6. Prove both Go dependency graphs are unchanged and run the final validation gates.
7. Set completed phase statuses to `done`, add dated outcomes to `Progress Notes`, move this plan to `review/`, and
   update `docs/exec-plans/index.md`.

## Validation Gates

- `test "$(uname -s)" = Linux` passes.
- `docker info --format '{{json .Runtimes}}' | rg '"sysbox-runc"'` finds the registered runtime.
- `stat -c '%a %u:%g %n' "$XDG_RUNTIME_DIR"` reports the invoking UID and no group/other permission bits.
- `make docker-build` succeeds before the throwaway image is built.
- Inspecting the throwaway image shows entrypoint
  `["/usr/bin/tini", "--", "/usr/local/bin/host-mcp-sidecar-spike"]` and no default command.
- `CGO_ENABLED=0 go -C tests/smoke build -o /tmp/codex-safe-host-mcp-spike \
  ./cmd/host-mcp-sidecar-spike` succeeds while the temporary helper exists.
- `CODEX_SAFE_RUN_HOST_MCP_SPIKE=1 go -C tests/smoke test . \
  -run '^TestHostMCPSidecarSpike$' -count=1 -v` produces seven explicit results; a skip is not PASS.
- `go -C tests/smoke test ./...` and `go -C tests/smoke vet ./...` pass while the harness exists.
- The evidence records the sidecar inspection, socket owner/mode, session and sidecar image IDs, generation inode
  before/after restart, the first same-name create outcome, die-to-name-creatable duration, the 60-second spike-only
  watchdog, raw cold-start, retry, lease-closure, sidecar-exit, recovery, and restored-data-path durations, and one
  result for requirements 1 through 7.
- `git diff -- go.mod go.sum tests/smoke/go.mod tests/smoke/go.sum` is empty.
- `test ! -e tests/smoke/host_mcp_sidecar_spike_test.go` and
  `test ! -e tests/smoke/cmd/host-mcp-sidecar-spike` pass after evidence capture.
- `docker ps -a --filter label=codex-safe.host-mcp-spike --quiet` prints nothing after cleanup.
- `docker images --filter label=codex-safe.host-mcp-spike --quiet` prints nothing after cleanup.
- The run-specific directory below `XDG_RUNTIME_DIR` no longer exists after cleanup.
- `make test`, `make check-docs`, and `git diff --check` pass after the temporary harness is removed.
- PASS outcome: all seven rows pass and the design explicitly permits the feature execution plan.
- FAIL outcome: the failing row contains reproducible evidence, the design is revised, and no feature plan exists.

## Risks and Constraints

- The result applies to the recorded kernel, Docker, Sysbox, filesystem, and image versions; retain those facts with
  the verdict.
- The helper emulates only the proposed byte path and lease lifecycle. It validates external assumptions, not the
  correctness of the future production implementation.
- The 60-second watchdog only bounds the experiment. A passing duration does not become a production timeout; the
  feature execution plan must derive those values and prove the design's ordering rule with an explicit safety margin.
- The spike must use the host Docker daemon for orchestration, but neither tested container may receive its socket.
- Creation and cleanup races must use explicit barriers and bounded event polling; scheduler sleeps are not evidence.
- Register cleanup before create/start calls and preserve inspect/log diagnostics when a bounded wait fails.
- The sidecar's one-mount rule requires installing the helper in the throwaway image rather than bind-mounting it.
- Retag only run-specific local images during immutable-image recovery; never alter the project's normal local tag.
- If Docker, Sysbox, `XDG_RUNTIME_DIR`, or registry access fails before a tested container runs, report an environment
  blocker and leave the plan active.

## Out of Scope

- Production relay, lease, listener, TOML discovery, launcher, Dockerfile, or CLI implementation.
- The host-MCP feature execution plan.
- A permanent host-MCP smoke test or Makefile target.
- MCP or HTTP protocol parsing; a nonce byte stream is sufficient for this external-assumption spike.
- Performance, load, fuzz, compatibility-matrix, or formal sandbox security testing.
- Docker Desktop, macOS, Windows, remote Docker daemons, and non-Sysbox session runtimes.
- Implementing the detached host relay fallback if the sidecar design fails.

## Progress Notes

- Add dated execution notes before moving the plan to `review/`.
- Record the tested environment, requirement 1-7 evidence table, raw monotonic timing measurements, overall PASS/FAIL
  verdict, deviations, and cleanup query results.
