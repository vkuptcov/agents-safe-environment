# Exec Plan: Codex Safe Infrastructure MVP

- Status: active
- Created: 2026-07-13
- Design: [`docs/design-docs/codex-safe.md`](../../design-docs/codex-safe.md)
- Scope:
  - `go.mod`, `cmd/codex-safe/`, `internal/gitproject/`, `internal/launcher/`
  - `container/`
  - `tests/smoke/`
  - `README.md`

## Objective

Build the smallest Go-based vertical slice that proves a linked Git worktree can run at its original absolute path
inside a Sysbox container and use an isolated nested Docker daemon without exposing the host Docker socket.

This plan validates the two highest-risk infrastructure assumptions before adding Codex installation, credentials,
interactive terminal behavior, and production hardening.

## Done Criteria

- `codex-safe` discovers a regular checkout or linked worktree from an explicit project path.
- A linked worktree, its primary checkout, and its common Git directory receive the required mount modes.
- The outer container runs with `sysbox-runc`, without privileged mode or the host Docker socket.
- A nested container bind-mounts the linked worktree and creates a file visible on the host.
- Git works in the mounted linked worktree while the primary checkout remains read-only.
- Nested Docker state is distinct from host Docker state.
- Focused Go tests and the real-host Sysbox smoke test pass.
- The README documents prerequisites, build steps, probe usage, evidence, and intentional omissions.

## Current Baseline

The repository contains the proposed design document but no Go module, launcher, container image, tests, or user-facing
setup instructions. The available local toolchain is Go 1.26.5 and Git 2.43.0.

The accessible host runs Docker Engine 28.3.3 with `overlay2` and cgroup v2. Docker has `sysbox-runc` registered, and
the installed runtime reports Sysbox CE 0.7.0. Actual Sysbox container startup, mount behavior, and nested Docker remain
unverified until the real-host smoke gates in Phases 7 and 8 run.

## Implementation Decisions

- Go module: use `github.com/vkuptcov/agents-safe-environment`, matching the configured Git remote.
- Dependencies: use the Go standard library only for the MVP host launcher.
- Path identity: preserve canonical host absolute paths as container targets.
- Worktree mounts: active worktree read-write, primary checkout read-only, and common `.git` read-write.
- Process execution: build argv slices and invoke Docker through `os/exec`; never interpolate a shell command.
- Probe CLI: use `codex-safe [--project PATH] [--image REF] -- COMMAND [ARG...]` for the MVP only.
- Probe requirement: require an explicit command so the MVP cannot be mistaken for the final Codex interface.
- Default image: use locally built `codex-safe-mvp:local`; allow an explicit override for testing.
- Image base: use Ubuntu 24.04 and pin the resolved base digest during implementation.
- Session identity: use an ephemeral unique name and a `codex-safe` label on every outer container.
- Failure policy: return preflight, Docker, daemon, and probe failures without any less-isolated fallback.
- Credentials: do not mount `~/.codex`; real credentials do not help prove the infrastructure hypothesis.
- Agent runtime: do not install or run Codex; authentication and agent networking are separate integration risks.

## Phases

### Phase 1: Git Project Discovery

Purpose: Derive canonical paths for regular checkouts and linked worktrees.
Status: done
Done when: the Go launcher describes both supported repository layouts without starting Docker.

1. Create `go.mod` for module `github.com/vkuptcov/agents-safe-environment` using Go 1.26.
2. Add `internal/gitproject/discovery.go` with a `Project` value containing:
   - requested working directory;
   - working-tree root;
   - Git directory;
   - common Git directory;
   - primary checkout root;
   - linked-worktree flag.
3. Resolve the requested path with `filepath.Abs` and `filepath.EvalSymlinks`.
4. Use `git -C <path> rev-parse --path-format=absolute` for repository paths; do not parse `.git` files.
5. Treat different Git and common-Git directories as the linked-worktree signal.
6. Derive the primary checkout only from a common directory ending in `.git`, then verify it exists.
7. Reject missing paths, non-worktrees, bare repositories, and unsupported external common directories.
8. Add `internal/gitproject/discovery_test.go` with temporary regular and linked repositories, covering a nested
   requested directory, a symlinked project path, spaces, and shell metacharacters.

### Phase 2: Mount Planning

Purpose: Convert discovered Git paths into a minimal and deterministic mount set.
Status: done
Done when: tests prove mount modes, path identity, ordering, duplicate removal, and conflict rejection.

1. Add `internal/launcher/plan.go` with typed `Mount` and `Plan` values.
2. Emit one read-write working-tree mount for a regular checkout.
3. For a linked worktree, emit mounts in this order:
   - primary checkout read-only;
   - common Git directory read-write;
   - active worktree read-write.
4. Preserve each canonical source path as its target path.
5. Remove exact duplicate mounts and reject duplicate targets with incompatible sources or modes.
6. Reject paths that cannot be represented safely by Docker `--mount` syntax.
7. Add `internal/launcher/plan_test.go` for regular, linked, duplicate, conflict, and path-order cases.

### Phase 3: Docker Invocation

Purpose: Turn a mount plan and probe command into a fail-closed Sysbox launch.
Status: done
Done when: tests prove the invocation uses Sysbox, omits unsafe access, and preserves the probe argv and exit status.

1. Add `internal/launcher/docker.go` with injectable command execution for tests.
2. Preflight Linux, a responsive local Docker daemon, the selected image, and registered `sysbox-runc`.
3. Generate a unique outer-container name and attach a project-owned session label.
4. Build `docker run` argv with `--rm`, `--runtime=sysbox-runc`, the working directory, and explicit mounts.
5. Append the selected image and untouched probe argv as separate arguments.
6. Propagate Docker or probe exit status without falling back to host Codex, `runc`, privileged mode, or a host socket.
7. Add `internal/launcher/docker_test.go` for preflight, safe argv, required flags, forbidden flags, and exit codes.

### Phase 4: Go CLI

Purpose: Expose the MVP discovery and launch flow through one small Go command.
Status: done
Done when: the built binary validates its flags, invokes the launcher, and returns actionable errors and exit codes.

1. Add `cmd/codex-safe/main.go` as a thin layer over discovery, planning, preflight, and execution.
2. Support only `--project`, `--image`, `--help`, and required `-- COMMAND [ARG...]` syntax.
3. Default `--project` to the current directory and `--image` to `codex-safe-mvp:local`.
4. Print actionable errors to stderr and keep ordinary output available to the probe.
5. Add focused CLI tests for missing command, flag parsing, help, dependency errors, and exit-code propagation.

### Phase 5: Nested Docker Image

Purpose: Provide a reproducible outer image that starts a private Docker daemon before the probe.
Status: done
Done when: the image starts its nested daemon with bounded readiness and executes the probe as the foreground command.

1. Add `container/Dockerfile` based on Ubuntu 24.04 and pin the resolved base digest.
2. Install only Git, Docker CLI and daemon, CA certificates, Bash, and a minimal init.
3. Add `container/entrypoint.sh` to start `dockerd` on its private Unix socket.
4. Wait for `docker info` with a bounded timeout.
5. Print daemon logs and exit nonzero when readiness fails.
6. Execute the probe argv without reparsing it through a generated shell command.
7. Ensure outer-container shutdown terminates the probe and nested daemon.
8. Validate the entrypoint with `bash -n` before attempting a Sysbox launch.

### Phase 6: Smoke Harness

Purpose: Create a deterministic real-host fixture and coordinate inspection of a live outer container.
Status: done
Done when: the harness starts a linked-worktree probe, pauses it for inspection, and cleans all labeled resources.

1. Add `tests/smoke/sysbox-linked-worktree.sh` with strict error handling and cleanup traps.
2. Create a temporary primary repository and linked worktree in paths containing spaces.
3. Configure local Git identity, add a baseline file, and create an initial commit.
4. Start a uniquely named and labeled sentinel container in the host Docker daemon.
5. Build `codex-safe-mvp:local` and the Go launcher.
6. Launch the probe from a nested linked-worktree directory and identify it through the unique session label.
7. Coordinate through ready and continue marker files so the host can inspect the outer container while it is alive.
8. Make every fixture, marker, container, and temporary repository removable by the cleanup trap.

### Phase 7: Sysbox and Worktree Proof

Purpose: Prove the live outer container has the intended isolation and Git mount behavior.
Status: to be done
Done when: runtime inspection and in-container assertions prove Sysbox isolation and linked-worktree Git access.

1. Inspect the live outer container and assert:
   - runtime is `sysbox-runc`;
   - privileged mode is false;
   - the expected mount sources, targets, and modes are present;
   - no host Docker socket is mounted.
2. Inside the outer container, run `git status` with the linked worktree as an explicit safe directory.
3. Stage a generated file and verify the staged state is visible from the host worktree.
4. Attempt to change the primary-checkout baseline and require a read-only filesystem failure.
5. Verify the common Git directory remains writable for linked-worktree index and lock updates.
6. Confirm the primary-checkout baseline remains unchanged after the probe continues.

### Phase 8: Nested Docker Proof

Purpose: Prove the inner daemon is independent and can pass the mounted worktree to a nested container.
Status: to be done
Done when: nested state is absent from the host daemon and its worktree marker persists with usable ownership.

1. Compare host and nested daemon IDs and require them to differ.
2. Verify the host sentinel is absent from nested `docker ps -a`.
3. Run a uniquely named nested container with the linked worktree mounted at the same absolute path.
4. Create a marker file from the nested container and verify it appears on the host.
5. Verify the marker is editable by the host user who ran the harness.
6. Verify the host sentinel is still running and no uniquely named nested object appears in host Docker.
7. Complete cleanup and require no labeled outer container or temporary test resource to remain.

### Phase 9: MVP Documentation

Purpose: Make the infrastructure proof reproducible without presenting it as a finished Codex launcher.
Status: to be done
Done when: a maintainer can build, run, verify, and correctly interpret the MVP from the README alone.

1. Add `README.md` with Linux, Docker, Git, Go, and Sysbox prerequisites.
2. Document image build, binary build, probe invocation, and smoke-test execution.
3. Document observed proof, security caveats, environment limitations, and deferred Codex integration.

## Validation Gates

- `gofmt -w cmd internal` completes, and a subsequent diff contains no Go formatting changes.
- `go test ./...` passes, including tests that create real temporary linked worktrees.
- `go vet ./...` passes.
- `go build -o /tmp/codex-safe ./cmd/codex-safe` succeeds.
- `bash -n container/entrypoint.sh tests/smoke/sysbox-linked-worktree.sh` succeeds.
- `docker build -t codex-safe-mvp:local -f container/Dockerfile .` succeeds.
- `tests/smoke/sysbox-linked-worktree.sh` passes on Linux with `sysbox-runc` registered.
- `git diff --check` reports no whitespace errors.
- `awk 'length($0) > 120 { print FILENAME ":" FNR ":" $0 }' README.md docs/*/*.md docs/*/*/*.md` prints nothing.
- `grep -RIn '[[:blank:]]$' README.md docs` prints nothing.

## Risks and Constraints

- The end-to-end gate requires a Linux host whose Docker daemon already has `sysbox-runc` registered.
- Sysbox UID/GID behavior varies by host filesystem; the smoke test must verify host-side editability.
- The image build and nested fixture container require registry access unless their images are already cached.
- Read-only primary-checkout enforcement and the writable nested `.git` override depend on actual mount ordering.
- Unit tests cannot prove kernel, Docker, or Sysbox mount behavior; the Phase 7 and Phase 8 smoke gates are mandatory.
- Runtime registration alone does not prove Sysbox container startup or nested Docker behavior; the smoke gates remain
  mandatory.
- This MVP is not yet a safe Codex release because credentials, resource limits, and production image policy are
  deliberately deferred.

## Out of Scope

- Mounting `~/.codex` or any other host home directory.
- Installing, authenticating, or running Codex.
- The final no-argument `codex-safe` user experience.
- CPU, memory, PID, disk, and nested-Docker cache policies.
- Persistent nested images, volumes, or build cache.
- Interactive TTY resize and complete signal and lifecycle coverage.
- Parallel sessions, stale-session cleanup commands, and host port publishing.
- macOS, Windows, Docker Desktop, rootless Docker, remote daemons, bare repositories, and external common Git dirs.
- Production image publication, signing, digest policy, installers, and release packaging.

## Progress Notes

- Add dated notes before moving this plan to `completed/`, including deviations from this plan and their rationale.
