# Exec Plan: Go Session Manager

- Status: active
- Created: 2026-07-14
- Design: [`docs/design-docs/go-session-manager.md`](../../design-docs/go-session-manager.md)
- Scope:
  - `cmd/codex-safe/`, `cmd/codex-safe-session/`
  - `internal/container/`, `internal/launcher/`, `internal/session/`
  - `container/`, `Makefile`
  - `tests/smoke/`
  - `README.md`, `docs/design-docs/`

## Objective

Replace first-command-owned outer containers with one detached Sysbox container per project and host UID. A Go
session manager must keep that container alive while any wrapped foreground command is running and let Docker remove
it after one fixed idle timeout.

The implementation must preserve the current mount, identity, nested-Docker, TTY, argv, and exit-code behavior while
removing random session discovery and the special first-command path.

## Done Criteria

- The launcher derives `codex-safe-<24-hex-project-key>` from host UID and canonical worktree root.
- The launcher inspects that exact name and validates the documented ownership, path, UID, and protocol labels.
- A new outer container starts detached with Sysbox and `--rm`; user commands never become its main process.
- The image uses `tini -- codex-safe-session serve` directly and contains no shell entrypoint.
- The Go `serve` process performs privileged identity bootstrap and supervises the nested Docker daemon.
- First and subsequent commands both run as `codex-safe-session run -- COMMAND ARG...` through `docker exec`.
- Interactive Bash keeps the outer container alive until Bash exits.
- With two commands active, either command can finish without interrupting the other or the nested Docker daemon.
- Zero active commands for five seconds makes the manager exit, dockerd stop, and Docker remove the outer container.
- Wrapper stdin, stdout, stderr, TTY behavior, argv boundaries, signals, working directory, and exit status are covered.
- Concurrent first launches create one outer container; mismatched deterministic-name ownership fails closed.
- No host runtime directory, control socket mount, lock file, host Docker socket, or privileged fallback is introduced.
- Focused Go tests, race tests, image build, shell checks, and the real-host Sysbox smoke test pass.
- Each completed phase is committed separately before the next phase starts.

## Current Baseline

The launcher currently creates a randomly named outer container and passes the first requested command as its main
process. Later invocations find that running container through project and UID labels and call raw `docker exec`.
When the first command exits, Docker removes the outer container even if a later exec command is still active.

`container/entrypoint.sh` configures the host-matching account, starts dockerd, waits for readiness, and then replaces
itself with the requested command. The image has no Go session binary, and the repository has no `internal/session/`
package.

Existing unit and Sysbox smoke coverage proves same-container reuse only while the first command remains alive. It
does not prove deterministic creation, wrapper registration, last-command lifetime, or idle removal.

## Implementation Decisions

- Session binary: add one `codex-safe-session` Go binary with `serve` and `run` modes.
- Entrypoint: invoke `tini -- codex-safe-session serve` directly; remove `container/entrypoint.sh` after cutover.
- Privileges: keep `serve` as root for account setup and dockerd supervision; run every user command as host UID/GID.
- Init: retain Tini only as PID 1, signal forwarder, and orphan reaper.
- Runtime dependencies: use the Go standard library only; test-only dependencies such as testify are allowed. Do not
  add a Docker SDK or RPC framework.
- Socket: use `/run/codex-safe/session.sock` inside the outer container only.
- Registration: a manager acknowledgement byte registers one wrapper connection; connection close unregisters it.
- Commands: keep argv out of the manager protocol and execute it only as the wrapper's direct child.
- Timeout: use one fixed five-second idle timeout at manager startup and after the final command.
- Project key: use the first 24 lowercase hex characters of SHA-256 over decimal UID, NUL, and canonical project root.
- Container name: use `codex-safe-<project-key>` without repeating the UID.
- Labels: use `codex-safe.managed=true`, project path, host UID, and manager protocol version `1`.
- Discovery: inspect only the deterministic name; labels validate it but do not drive launcher discovery.
- Creation race: rely on Docker's atomic name ownership, then inspect and validate the winning container.
- Runtime cutover: change launcher and image entrypoint behavior in the same phase so their contracts never diverge.
- Phase commits: commit every phase after its validation passes; do not squash phases during implementation.

## Phases

### Phase 1: Manager Core

Purpose: Implement the container-local active-command registry independently of Docker orchestration.
Status: done
Done when: a Unix-socket manager acknowledges commands, counts connections exactly once, and exits after one idle
timeout with zero active commands.

1. Add `internal/session/contract.go` for the socket path, protocol version, acknowledgement, and default timeout.
2. Add `internal/session/manager.go` with a documented configuration type for socket path, idle timeout, and logging.
3. Create the private runtime directory and Unix listener with the ownership and modes required by the design.
4. Guard `active`, timer transitions, and committed shutdown with one mutex.
5. Acknowledge an accepted connection only after it is counted; unregister it exactly once on close.
6. Close the listener, remove the socket, and return cleanly when the idle timer fires with `active == 0`.
7. Add manager tests for startup idle, one and two connections, reconnect during idle, shutdown races, and signals.

Commit: `feat: add session manager core`

### Phase 2: Command Wrapper and Session CLI

Purpose: Register arbitrary foreground commands without changing their terminal or process result contract.
Status: done
Done when: `codex-safe-session run -- COMMAND` holds registration for the direct child's lifetime and returns its
status, while `serve` exposes the manager.

1. Add `internal/session/runner.go` with documented inputs for streams, environment, socket, and startup timeout.
2. Retry the local socket during bounded container bootstrap and require manager acknowledgement before child start.
3. Start untouched argv as a direct child with inherited working directory, environment, and attached streams.
4. Forward termination signals, wait for the child, and translate normal and signal exits into the wrapper status.
5. Release registration on start failure, normal exit, signal exit, and wrapper cancellation.
6. Add `cmd/codex-safe-session/main.go` with strict `serve` and `run -- COMMAND [ARG...]` parsing.
7. Add focused CLI and runner tests using helper subprocesses for argv, I/O, signals, and exit statuses.
8. Update `make build` so both Go binaries are written under `bin/`.

Commit: `feat: add session command wrapper`

### Phase 3: Go Container Entrypoint

Purpose: Replace shell bootstrap and supervision behavior with tested Go components before the atomic image cutover.
Status: done
Done when: `codex-safe-session serve` can reconcile identity, prepare the home, start a ready dockerd, run the manager,
and stop dockerd without shell orchestration.

1. Add `internal/container/config.go` to parse and validate host identity, home, and readiness inputs.
2. Reconcile users and groups through explicit, injectable subprocess argv; never invoke a shell or reconstruct
   commands.
3. Create the home, Bash configuration, and sudoers policy with Go filesystem APIs.
4. Add dockerd startup with fixed argv, captured diagnostics, and readiness through its private Unix HTTP socket.
5. Add a supervisor that runs the manager as root, owns dockerd, and performs bounded shutdown on idle, signal, or
   crash.
6. Test configuration, identity conflicts, filesystem modes, readiness timeout, daemon failure, and shutdown with fakes.
7. Add a pinned Go build stage, install the binary in the runtime image, and keep the final image free of build tools.
8. Keep the old shell selected only until Phase 4 switches launcher and image contracts atomically.

Commit: `feat: add Go container supervisor`

### Phase 4: Atomic Runtime Cutover

Purpose: Make the detached manager container and wrapped Docker exec path the only launcher lifecycle.
Status: done
Done when: both a newly created session and a reused session execute the requested command through the same wrapper,
and no user command owns the outer container lifecycle.

1. Add deterministic project-key and container-name helpers with the exact documented SHA-256 input and example test.
2. Replace random-name and label-list discovery with exact-name inspect plus full label and state validation.
3. Build detached `docker run --rm` arguments with Sysbox, existing mounts and identity environment, and new labels.
4. Route first and subsequent commands through one wrapper-prefixed `docker exec` argument builder.
5. Handle a concurrent name conflict by inspecting the winner; reject mismatched labels and retry committed shutdown
   once after bounded name release.
6. Set the image entrypoint to `tini -- codex-safe-session serve` and delete `container/entrypoint.sh`.
7. Remove the obsolete random generator, shell readiness marker, main-command arguments, and first-command branch.
8. Update launcher and Go-entrypoint tests for detached creation, exact inspect, wrapper exec, failures, and cleanup.

Commit: `feat: manage project container sessions`

### Phase 5: Concurrency and Sysbox Proof

Purpose: Prove the new lifetime semantics against a real nested Docker daemon and overlapping commands.
Status: done
Done when: the smoke harness proves one project container survives either command exit, shares nested state, and is
removed only after the final command and idle timeout.

1. Update the smoke harness to locate and inspect the deterministic name and exact documented labels.
2. Start two blocking wrapped commands in one project and record their outer hostname and nested daemon ID.
3. Release the first command and prove the second command, outer container, and nested daemon remain alive.
4. Release the second command and prove the outer container remains during idle then disappears after five seconds.
5. Start two initial callers concurrently and prove Docker creates exactly one outer container.
6. Exercise name-label mismatch and shutdown-registration races without executing the command in the wrong session.
7. Preserve existing linked-worktree mounts, ownership, Compose, Git, Cyrillic, colors, and daemon-isolation assertions.
8. Cover interactive Bash, Ctrl-C, piped stdin, nonzero exit, and cleanup of every labeled test resource.

Commit: `test: prove shared session lifecycle`

### Phase 6: Documentation and Review Handoff

Purpose: Make the implemented lifecycle understandable, reproducible, and ready for owner review.
Status: to be done
Done when: user documentation matches verified behavior, all validation gates pass, and this plan is moved to review.

1. Update `README.md` with the deterministic name, labels, wrapper semantics, idle timeout, and diagnostic commands.
2. Document that Bash is active until it exits and detached background work does not independently retain the session.
3. Update design docs only for implementation deviations discovered during validation.
4. Add comments to non-obvious Go fields, timer transitions, Docker conflict handling, and Go supervision behavior.
5. Run every validation gate below and record dated results in `Progress Notes`.
6. Set all completed phase statuses to `done`, set plan status to `in review`, and move this file to `review/`.

Commit: `docs: document managed project sessions`

## Validation Gates

- `make build` succeeds and writes `bin/codex-safe` plus `bin/codex-safe-session`.
- `make test` passes all Go tests, vet checks, and shell syntax checks.
- `go test -race ./internal/session ./internal/launcher ./cmd/codex-safe ./cmd/codex-safe-session` passes.
- `gofmt -l cmd internal` prints no paths.
- `make docker-build` builds `codex-safe-mvp:local` with the session binary installed.
- `docker image inspect codex-safe-mvp:local --format '{{json .Config.Entrypoint}}'` reports Tini and the Go `serve`
  command without a shell script.
- `docker run --rm --entrypoint /usr/local/bin/codex-safe-session codex-safe-mvp:local --help` exits zero.
- `test ! -e container/entrypoint.sh` succeeds after the atomic cutover.
- `make test-smoke-go` passes on the supported real Sysbox host.
- After the smoke test, `docker ps -a --filter label=codex-safe.managed=true --quiet` prints nothing.
- `rg -n 'docker container ls|--filter.*project-path|codex-safe\.session' internal/launcher` prints nothing.
- `rg -n 'XDG_RUNTIME_DIR|control.*mount|create\.lock' cmd internal container` prints nothing.
- `git diff --check` reports no whitespace errors.
- All Markdown lines changed by the implementation are at most 120 characters.

## Risks and Constraints

- Wrapper signal and TTY behavior is the highest-risk unit boundary; prove it with both focused tests and a real PTY.
- Docker may keep an exec process alive after its host CLI disconnects. This is intentional while its command runs.
- The five-second timeout is deliberately fixed for the MVP and can expose scheduler-sensitive races if tests poll
  imprecisely; synchronize on manager events and use bounded waits.
- Docker names are daemon-global. Always validate every label before reusing a deterministic-name occupant.
- Launcher and image-entrypoint lifecycle changes are incompatible when applied separately; Phase 4 must be one commit.
- The root Go manager must terminate dockerd without weakening Sysbox or mount isolation. It must never execute user
  command argv; Docker exec runs wrappers and commands as the recreated host UID/GID.
- Tini remains necessary even with a Go entrypoint because PID 1 must reap orphaned descendants from nested workloads.
- Legacy random-name sessions are not migrated. Close them before using the deterministic-name launcher.
- The current local Go environment may require unsetting an externally forced `GOROOT`; that is an environment issue,
  not permission to weaken repository validation.

## Out of Scope

- Configurable idle timeout or persistent idle sessions.
- Session list, status, stop, resume, or terminal-reattachment commands.
- Keeping the outer container alive only for detached background or nested containers.
- Host-visible sockets, runtime directories, lock files, or heartbeats.
- Migration or automatic cleanup of legacy random-name containers.
- Replacing the Docker CLI with the Docker Go SDK.
- Codex installation, `~/.codex` mounting, resource limits, port forwarding, or network policy changes.
- Supporting non-Linux hosts, Docker Desktop, remote Docker daemons, or non-Sysbox runtimes.

## Progress Notes

- 2026-07-14: Phase 5 completed; the Sysbox smoke proves overlapping command survival, final-command idle removal,
  deterministic labels, and concurrent first callers. The scenario is now maintained by `make test-smoke-go`.
- 2026-07-14: Phase 4 completed in `50ff335`; deterministic naming, exact-name inspection, detached lifecycle,
  wrapper-prefixed exec, Go entrypoint cutover, and shell entrypoint removal are implemented and tested.
- 2026-07-14: Phase 3 moved identity, home, sudoers, dockerd readiness, diagnostics, and shutdown behavior into tested
  Go components. Full race tests, `make test`, image build, and the image-installed binary help check pass.
- 2026-07-14: Phase 2 added the command wrapper and strict session CLI. Both binaries build under `bin/`; focused race
  tests and `make test` cover argv, streams, environment, working directory, registration lifetime, signals, and exits.
- 2026-07-14: Phase 1 added the manager protocol and Unix-socket state machine. `make test`, focused vet, and
  `go test -race ./internal/session` pass with an external `GOROOT` unset and a writable Go build cache.
- 2026-07-14: revised before implementation so the Go manager is the image entrypoint and owns privileged bootstrap
  and dockerd supervision; the obsolete shell entrypoint will be deleted during the atomic cutover.
- Add dated notes before moving the plan to `completed/`, including validation results, phase commits, deviations,
  and any intentionally deferred work.
