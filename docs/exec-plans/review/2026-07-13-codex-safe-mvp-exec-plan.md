# Exec Plan: Codex Safe Infrastructure MVP

- Status: in review
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
and production hardening. Minimal interactive terminal attachment was added after review feedback exposed that a bare
`bash` probe received no stdin and exited immediately.

## Done Criteria

- `codex-safe` discovers a regular checkout or linked worktree from an explicit project path.
- A linked worktree, its primary checkout, and its common Git directory receive the required mount modes.
- The outer container runs with `sysbox-runc`, without privileged mode or the host Docker socket.
- A nested container bind-mounts the linked worktree and creates a file visible on the host.
- Git works in the mounted linked worktree while the primary checkout remains read-only.
- Nested Docker state is distinct from host Docker state.
- Focused Go tests and the real-host Sysbox smoke test pass.
- An interactive `bash` probe receives terminal input and exits cleanly.
- The README documents prerequisites, build steps, probe usage, evidence, and intentional omissions.

## Starting Baseline

The repository contains the proposed design document but no Go module, launcher, container image, tests, or user-facing
setup instructions. The available local toolchain is Go 1.26.5 and Git 2.43.0.

The accessible host ran Docker Engine 28.3.3 with `overlay2` and cgroup v2. Docker had `sysbox-runc` registered, and
the installed runtime reported Sysbox CE 0.7.0. Sysbox container startup, mount behavior, and nested Docker were still
unverified at the start of implementation.

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

1. Add `tests/smoke/sysbox_linked_worktree_test.go` with explicit assertions and automatic test cleanup.
2. Create a temporary primary repository and linked worktree in paths containing spaces.
3. Configure local Git identity, add a baseline file, and create an initial commit.
4. Start a uniquely named and labeled sentinel container in the host Docker daemon.
5. Build `codex-safe-mvp:local` and the Go launcher.
6. Launch the probe from a nested linked-worktree directory and identify it through the unique session label.
7. Coordinate through ready and continue marker files so the host can inspect the outer container while it is alive.
8. Make every fixture, marker, container, and temporary repository removable by `t.Cleanup`.

### Phase 7: Sysbox and Worktree Proof

Purpose: Prove the live outer container has the intended isolation and Git mount behavior.
Status: done
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
Status: done
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
Status: done
Done when: a maintainer can build, run, verify, and correctly interpret the MVP from the README alone.

1. Add `README.md` with Linux, Docker, Git, Go, and Sysbox prerequisites.
2. Document image build, binary build, probe invocation, and smoke-test execution.
3. Document observed proof, security caveats, environment limitations, and deferred Codex integration.

### Phase 10: Interactive Terminal Attachment

Purpose: Make the explicit probe interface usable for an interactive shell without breaking automation.
Status: done
Done when: a `bash` probe stays open in a terminal, accepts commands, and exits without leaving an outer container.

1. Always attach host stdin to `docker run` so redirected input reaches the probe.
2. Add `--tty` only when both host stdin and stdout are terminal devices.
3. Replace the entrypoint with the probe after daemon readiness so the probe owns the foreground terminal.
4. Add unit coverage for TTY and non-TTY Docker arguments.
5. Verify a real PTY session and piped stdin against the target linked-worktree project.

### Phase 11: Compose and Shell Tooling

Purpose: Provide the basic project commands required by the first real interactive workflow.
Status: done
Done when: Compose V2, `make`, `less`, and `rg` are available, and a Compose service starts on the nested daemon.

1. Install Ubuntu's `docker-compose-v2`, `make`, `less`, and `ripgrep` packages in the outer image.
2. Verify each command exists in the real-host smoke probe.
3. Start and stop an Alpine service with `docker compose up -d` and `docker compose down`.
4. Prove the Compose-managed container is invisible to host Docker and leaves no object after outer shutdown.
5. Document the included tools and Compose usage in the README.

### Phase 12: Local Build Layout and Bash Completion

Purpose: Standardize local build output and make interactive Make workflows behave like a normal development shell.
Status: done
Done when: builds write only to `bin/`, Make targets run checks and rebuild the image, and Bash completes Make targets.

1. Ignore `bin/` and make `bin/codex-safe` the documented local binary path.
2. Add `Makefile` targets named `build` and `test`.
3. Add a `docker-build` target for rebuilding the local `codex-safe-mvp:local` image.
4. Install `bash-completion` in the outer image.
5. Copy a project-owned Bash startup file into the ephemeral probe home before starting the interactive command.
6. Source the system completion framework from that startup file.
7. Extend the smoke probe to load and verify the registered Make completion handler.

### Phase 13: Host Account Name Parity

Purpose: Make account names inside the probe match the invoking host identity instead of image-defined names.
Status: done
Done when: the probe has the host login name, primary group name, UID, and GID.

1. Resolve the invoking user's login and primary group names from the host account database.
2. Validate and pass both names alongside the existing UID and GID in the outer-container environment.
3. Reconcile image-defined passwd and group entries with the host identity before dropping privileges.
4. Fail on unsupported names or conflicting name-to-ID mappings instead of using an incorrect identity.
5. Extend unit tests for Docker arguments and invalid names.
6. Require the real-host smoke probe's `whoami` and `id -gn` output to equal the host values.

### Phase 14: Global Git Config and UTF-8 Shell

Purpose: Preserve normal host Git identity settings and support Cyrillic text in the interactive environment.
Status: done
Done when: Git reads the host global config through a read-only mount and the probe uses a UTF-8 locale.

1. Resolve an existing `$HOME/.gitconfig` to a canonical regular file without requiring it to exist.
2. Mount that file read-only at `.gitconfig` inside the container-local home.
3. Do not implicitly mount files referenced by Git includes, credential helpers, or the rest of the host home.
4. Set the image's default locale to `C.UTF-8` without installing a language-specific locale.
5. Add unit coverage for optional config discovery, path validation, and Docker mount arguments.
6. Extend the real-host smoke probe to read a deterministic global setting and reject writes to the config.
7. Require `locale charmap` to report UTF-8 and round-trip Cyrillic text through the project mount.

### Phase 15: Host Home Path Parity

Purpose: Preserve absolute paths embedded in shell, Git, and tool configuration without exposing the host home.
Status: done
Done when: the probe's `$HOME` and passwd home equal host `$HOME`, while the directory remains container-local.

1. Resolve and validate host `$HOME` as a canonical absolute path distinct from `/`.
2. Pass that path to the entrypoint and create it inside the outer container's writable layer.
3. Set the recreated account's passwd home and the probe's `HOME` environment variable to the host path.
4. Target the read-only `.gitconfig` mount at the same absolute host path.
5. Keep the full host home directory unmounted.
6. Extend unit tests for the home environment and config mount target.
7. Require the real-host smoke probe to verify `HOME`, the passwd entry, and the exact config mount path.

### Phase 16: Interactive Terminal Colors

Purpose: Make the interactive probe visually usable while keeping redirected output free of forced ANSI escapes.
Status: done
Done when: terminal-aware tools see 256-color capability and interactive Bash uses color-aware defaults.

1. Set the image's default `TERM` to `xterm-256color`, whose terminfo entry is available in the image.
2. Configure a colored user, host, and working-directory prompt only for interactive Bash sessions.
3. Add `--color=auto` aliases for `ls` and `grep` so redirected output remains plain.
4. Extend the smoke probe to require 256-color capability, prompt colors, and the color-aware `ls` alias.
5. Verify a real PTY displays ANSI-colored prompt output.

### Phase 17: Container-Local Sudo

Purpose: Allow the interactive user to administer the ephemeral outer container without a configured password.
Status: done
Done when: the recreated host user can run `sudo --non-interactive` as container root.

1. Install Ubuntu's `sudo` package in the outer image.
2. Generate a dedicated sudoers fragment for the validated host account during entrypoint bootstrap.
3. Grant `NOPASSWD: ALL`, set the fragment mode to `0440`, and validate it with `visudo` before launching the probe.
4. Keep the policy file owned and writable only by container root.
5. Require the smoke probe to obtain UID `0` through non-interactive sudo.
6. Verify `sudo apt-get update` in a real codex-safe session.
7. Document that sudo reaches Sysbox-container root, not host root.

### Phase 18: Active Project Container Reuse

Purpose: Route later commands for a worktree into its already-running outer container without changing `--rm` cleanup.
Status: done
Done when: a second invocation for one live worktree uses the same outer container and nested Docker daemon.

1. Add the canonical worktree root to the launch plan as the stable managed-project identity.
2. Label new containers with `codex-safe.project-path` and `codex-safe.host-uid` in addition to the session label.
3. Search only running containers by all three labels before creating a new session.
4. If exactly one container matches, wait for its bootstrap readiness marker and run the command through `docker exec`.
5. Preserve stdin, optional TTY, host UID/GID, host `HOME`, and the newly requested working directory during exec.
6. Keep `docker run --rm` when no container matches, and reject multiple matches instead of choosing arbitrarily.
7. Unit-test label construction, lookup, exec argv, readiness waiting, and ambiguous matches.
8. Extend the Sysbox smoke test to prove the second invocation sees the same hostname and nested daemon ID.

## Validation Gates

- `gofmt -w cmd internal` completes, and a subsequent diff contains no Go formatting changes.
- `go test ./...` passes, including tests that create real temporary linked worktrees.
- `go vet ./...` passes.
- `go build -o /tmp/codex-safe ./cmd/codex-safe` succeeds.
- `bash -n container/bashrc` succeeds.
- `docker build -t codex-safe-mvp:local -f container/Dockerfile .` succeeds.
- `make test-smoke-go` passes on Linux with `sysbox-runc` registered.
- A real PTY probe accepts `pwd`, `docker info`, and `exit`; a non-TTY pipe reaches `bash` stdin.
- The smoke probe executes Compose V2, `make`, `less`, and `rg`, and manages a nested Compose service.
- The smoke probe reports the same login and primary group names as the invoking host account.
- The smoke probe reads the mounted host Git config, cannot modify it, and round-trips Cyrillic under UTF-8.
- The smoke probe's `HOME` and passwd home equal host `$HOME` without a broad host-home mount.
- The smoke probe sees 256 terminal colors and loads the interactive colored prompt and command aliases.
- The smoke probe obtains container UID `0` through passwordless sudo without gaining host-root access.
- A second invocation for the live smoke worktree reuses its hostname, host identity, and nested Docker daemon.
- `make build` writes `bin/codex-safe`; `make test` and `make docker-build` pass; the smoke probe registers Make target
  completion.
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
- Signal and lifecycle coverage beyond normal terminal exit and Docker's attached-session behavior.
- Independent parallel outer sessions for one worktree, stopped-session resume, stale-session cleanup commands, and
  host port publishing.
- macOS, Windows, Docker Desktop, rootless Docker, remote daemons, bare repositories, and external common Git dirs.
- Production image publication, signing, digest policy, installers, and release packaging.

## Progress Notes

- 2026-07-13: Implemented Phases 1 through 8 as separate commits and kept the launcher dependency-free outside the Go
  standard library. The host-side launcher never builds a shell command and never mounts the host Docker socket.
- 2026-07-13: The first real Sysbox write probe exposed host-ownership and nested-socket access requirements. The
  entrypoint now keeps `dockerd` as container root, grants only the invoking UID/GID access to its private socket, and
  runs the probe with that identity and an ephemeral home directory. The worktree, common Git directory, and nested
  marker remained host-editable in the final smoke test.
- 2026-07-13: Ubuntu's nested `runc` 1.3.4 could not bind a Sysbox mount to a target path containing spaces and failed
  while reapplying `MS_NOATIME`. The image now pins official `crun` 1.28 binaries by SHA-256 for `amd64` and `arm64`
  and uses `crun` as the nested daemon's default runtime. This preserved identical absolute paths without a mount
  bridge or a less-isolated outer container.
- 2026-07-13: All validation gates passed on Docker Engine 28.3.3 with Sysbox CE 0.7.0, cgroup v2, and `overlay2`.
  The final smoke test proved the Sysbox runtime, mount modes, read-only primary checkout, writable linked-worktree
  Git metadata, distinct daemon IDs, host-sentinel isolation, nested bind access, host UID/GID ownership, and cleanup.
- 2026-07-13: Added the MVP README and moved this plan to review. Codex installation, `~/.codex`, resource limits,
  and production image policy remain intentionally deferred to later plans.
- 2026-07-13: Review feedback showed that `codex-safe -- bash` exited immediately because `docker run` did not attach
  stdin and the entrypoint started the probe as a background child. Phase 10 added automatic PTY detection,
  unconditional stdin attachment, and a foreground `exec` after daemon readiness. The exact reported project path
  passed interactive input, nested `docker info`, Ctrl-C, clean `exit`, piped input, and the full smoke harness.
- 2026-07-13: Added Compose V2, `make`, `less`, and `rg` after the first interactive project run exposed the missing
  tools. The smoke harness now starts a real nested service with `docker compose up -d`, verifies host-daemon
  invisibility, and removes the Compose project before shutdown.
- 2026-07-13: Standardized local builds under `bin/`, added `make build` and `make test`, and enabled Make target
  completion through a project-owned `.bashrc` in the ephemeral home. The smoke probe now requires the `_make`
  completion handler to load successfully, and an interactive PTY probe expands `make bu<Tab>` to `make build`.
- 2026-07-14: Added `make docker-build` as the documented command for rebuilding the local MVP image.
- 2026-07-14: Replaced image-defined account names with the invoking host login and primary group names. Docker
  arguments now carry names and numeric IDs, the entrypoint reconciles passwd and group entries, and the smoke test
  requires `whoami` and `id -gn` to match the host.
- 2026-07-14: Added an optional read-only mount for host `$HOME/.gitconfig` and enabled `C.UTF-8` in the image. The
  smoke fixture now proves global Git config visibility, write protection, UTF-8 locale selection, and Cyrillic text.
- 2026-07-14: Recreated host `$HOME` at the same absolute path in the outer container's writable layer. The account,
  process environment, and `.gitconfig` target now share that path without mounting the complete host home directory.
- 2026-07-14: Enabled `xterm-256color` and interactive Bash colors. The prompt distinguishes identity and working
  directory, while `ls` and `grep` use automatic color modes that remain disabled for redirected output.
- 2026-07-14: Installed `sudo` and generated a validated `NOPASSWD` policy for the recreated host account. The smoke
  probe now proves non-interactive escalation to Sysbox-container root and rejects user writes to the policy file.
- 2026-07-14: Added canonical project-path and host-UID labels. Later invocations for the same live worktree now wait
  for bootstrap readiness and execute through `docker exec`, while the main session retains automatic `--rm` cleanup.
