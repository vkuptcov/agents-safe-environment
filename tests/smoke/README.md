# Sysbox smoke tests

This directory contains real-host integration tests for `codex-safe`, `claude-safe`, and `agents-safe`. Lifecycle
probes run arbitrary Bash through the public `agents-safe` command, and product assertions run both agent launchers
directly, so the suite drives the same launcher create, reuse, and exec path a user runs. The test creates a managed
container with the `sysbox-runc` runtime, starts a private Docker daemon inside that container, and exercises a
linked Git worktree through multiple concurrent client commands.

The smoke test checks the boundaries between the host, the managed Sysbox container, and containers started by
the nested Docker daemon. It complements unit tests; it is not a replacement for them or a general proof that
the sandbox cannot be escaped.

## Quick start

Run the complete scenario from the repository root:

```bash
make test-smoke-go
```

The target:

1. builds `bin/codex-safe`, `bin/claude-safe`, `bin/agents-safe`, and `bin/agents-safe-session`;
2. builds the `agents-safe-mvp:local` image;
3. enables the opt-in smoke test with `CODEX_SAFE_RUN_SYSBOX_SMOKE=1`;
4. runs every `TestSysbox` scenario (linked worktree, worktree-metadata guard, both product launchers,
   shared-session coexistence, `agents-safe bash`, configured mounts, and project-image selection) without the Go
   test cache.

The test requires:

- a Linux host;
- a reachable Docker Engine daemon;
- `sysbox-runc` registered as a Docker runtime;
- Git and Go 1.26 or newer on the host;
- registry access to pull uncached Dockerfile inputs and the pinned nested workload image.

The uv reuse scenarios additionally require host `uv` and `python3`, plus registry access to the approved digest-pinned
uv/Python image. They skip with that exact missing-tool prerequisite if the host does not provide it; a skipped scenario
is not real uv reuse evidence.

Check the registered Docker runtimes with:

```bash
docker info --format '{{json .Runtimes}}'
```

For a direct invocation, build the prerequisites first and run the test from its own Go module:

```bash
make build docker-build
CODEX_SAFE_RUN_SYSBOX_SMOKE=1 \
    go -C tests/smoke test . -run TestSysboxLinkedWorktreeGo -count=1 -v
```

Ordinary validation does not launch Sysbox:

```bash
make test
```

`make test` compiles, tests, and vets both the application module and this smoke-test module. The real-host test
sees that `CODEX_SAFE_RUN_SYSBOX_SMOKE` is unset and reports a skip before it connects to Docker.

## Architecture

The Go test owns host orchestration. Small embedded Bash workloads run only where shell and CLI behavior must be
observed inside the managed environment.

```text
Host Go test
├── launcherHarness ── starts all three public launchers as real host processes
├── dockerHarness ──── inspects the host daemon through the Moby client
├── temporary Git primary checkout + linked worktree
└── host sentinel container
             │
             │ agents-safe --project <linked worktree> -- <command>
             ▼
Managed container (sysbox-runc, not privileged)
├── agents-safe-session manager
├── recreated host user, group, and home path
├── mounted project, writable common Git state, read-only worktree registry, and read-only .gitconfig
├── explicitly configured host directories mounted read-write at the same paths
└── private Docker daemon using crun
             │
             ├── docker run container
             └── Docker Compose service
```

The Go test accesses the host Docker socket. The socket is deliberately not mounted into the container.
Commands in the container talk to the private nested daemon instead.

## Scenario

`TestSysboxLinkedWorktreeGo` runs one ordered end-to-end scenario:

1. Create a temporary primary Git checkout, a linked worktree, a nested project directory, and a synthetic host
   home. Paths contain spaces so path quoting is exercised.
2. Create a host sentinel container. The nested daemon must never be able to see it.
3. Start the environment probe from the nested project directory. This first client creates the deterministic
   container and remains connected at a synchronization barrier.
4. Run the worktree probe through a second client. It must reuse the container, modify and stage a linked
   worktree file, write the common Git directory, and fail to modify the read-only primary checkout.
5. Start a nested-Docker probe through another client. It runs a container with a project bind mount and starts
   a Compose service on the private daemon.
6. Inspect the live container from the host and validate its labels, runtime, working directory, privilege
   mode, and mounts.
7. Start another command and prove that it sees the same container hostname and nested daemon. Release clients one at
   a time and prove that the remaining clients and container stay alive.
8. Release the final client, observe the idle grace period, wait for automatic container removal, and
   validate nested cleanup and host-side file ownership.
9. Start two first callers concurrently against a fresh session and prove that exactly one deterministic managed
   container is created.

`TestSysboxAgentsSafeBash` starts `agents-safe bash -c ...` without a separator and verifies that Bash runs in the
selected project before the idle lifecycle removes the container.

`TestSysboxWorktreeMetadataGuard` starts a primary-checkout session before `.git/worktrees` exists and proves cold
launch materializes the read-only guard. It then creates two worktrees on the host, keeps sibling roots hidden, and
attempts ordinary prune, elevated admin-directory deletion, and detached worktree creation from both primary and linked
sessions. Host worktree state, target paths, and sibling usability must survive unchanged, while status, add, commit,
shared-ref updates, and host ownership remain functional in each selected checkout. Docker inspection verifies common
`.git` `rw`, registry `ro`, a linked-only active-GitDir `rw` override, and no sibling-root bind. Explicit `-b` is not the
clean rejection probe because Git may create that ordinary branch ref before its guarded registry write fails.

`TestSysboxAgentsSafeWithoutCodexHome` omits the fixture `.codex` directory and verifies that the real container
has no Codex-home bind mount, carries the `absent` compatibility label, and does not pass `CODEX_HOME` to the command.

`TestSysboxClaudeAndCodexShareOneSession` holds one session open, runs both product launchers through it, verifies
both installation volumes are read-only, writes Claude user configuration through the native host mounts, and proves
only one managed container exists throughout.

`TestSysboxProjectEnvironment` creates a fixture `.agents-safe/Dockerfile` and drives the public launcher without
`--image`. It verifies the production cold-build path, the project-provided executable, ordinary active-session reuse
after a Dockerfile change, a BuildKit-backed rebuild on the next cold launch, and image cleanup.

`TestSysboxConfiguredMount` writes a local `.agents-safe/config.toml`, launches through the public `agents-safe`
command with an explicit image, and verifies that an external directory is visible read-write at the same absolute
path with `rprivate` propagation.

`TestSysboxGoHostCaches` builds a test-only project image with the pinned Go toolchain, then verifies that configured
`go_build` and `go_modules` sources are same-path writable binds, override image-owned Go cache defaults, and retain
container writes for the host after a cold-session cleanup. It seeds a local `file://` module proxy through native Go,
then proves the reused command, the host, and a new cold Sysbox session build offline from the shared cache. The test
also verifies that nested Docker receives neither Go cache variable. `TestSysboxGoCacheConfigMismatch` proves that a
changed cache configuration rejects reuse and leaves the active session untouched. The base runtime image intentionally
remains Go-free.

`TestSysboxConfiguredUVCacheBind` verifies the launcher-side uv boundary through the real base image: `kind = "uv"`
is a same-path writable bind, the managed command receives `UV_CACHE_DIR`, the diagnostic label identifies the source,
and a container write remains host-owned. It also proves a project-image build receives no cache variable and nested
Docker receives no `UV_CACHE_DIR`. It deliberately does not put uv or Python in the base image.

`TestSysboxUVHostCacheReuse` uses the approved digest-pinned uv/Python image only in a temporary project image. It
proves native-to-container and container-to-native offline reuse at one exact loopback index URL, empty-cache control
failures, cold-session reuse, and host ownership. `TestSysboxConcurrentUVCacheWorktrees` proves two worktrees can
write one cache concurrently without a launcher lock; `TestSysboxUVCacheConfigMismatch` proves a live session is left
untouched when its cache identity changes.

## Probe synchronization

Long-running probes communicate through files in the temporary linked worktree. That directory is visible to both
the host test and commands inside the container.

- `*.report` contains observed facts in `key=value` form.
- `*.ready` tells the host that a probe reached its inspection barrier.
- `*.release` tells the probe to clean up and exit.

The shell probes collect facts; expected values live in Go assertions. This keeps failure messages specific and
prevents the same expectation from being encoded once in Bash and again in Go.

`waitForFile` also watches the launcher process. If a command exits before creating its marker, the test fails
immediately and includes the command's captured stdout and stderr instead of waiting for the full timeout.

## What is verified

| Area | Assertions |
|---|---|
| Host identity | User and primary group names match the host; commands run as the host UID and GID. |
| Home directory | `$HOME` and the passwd entry preserve the absolute host home path inside the container. |
| Root access | `sudo --non-interactive` reaches UID 0; the sudoers policy is mode `0440` and not user-writable. |
| Git config | The synthetic host `.gitconfig` is visible globally, mounted read-only, and unchanged after the run. |
| Terminal | UTF-8, Cyrillic round-trip, 256 colors, the colored prompt, and the color-aware `ls` alias work. |
| Tools | `less`, `make`, `rg`, Docker Compose, and Make completion are available independently. |
| Worktree | Git works from both checkout kinds; files can be committed and shared refs updated. |
| Metadata guard | Hidden siblings survive prune and elevated deletion; in-session worktree creation fails cleanly. |
| Mount policy | Common Git state is writable, the registry is read-only, and only the active linked GitDir is overridden writable. |
| Configured mount | An explicitly configured external directory is mounted read-write at the same absolute path. |
| Mount isolation | Mounts use `rprivate`; no source or destination is the host Docker socket. |
| Container | It uses `sysbox-runc`, is not privileged, preserves its working directory, and has exact labels. |
| Nested daemon | Its ID differs from the host daemon, it uses `crun`, and it cannot see the sentinel. |
| Nested workloads | `docker run` writes through a bind mount and the Compose service reaches the running state. |
| Session reuse | Overlapping clients share one container hostname, one nested daemon, and one managed container. |
| Product coexistence | Codex and Claude Code use independent state/installations while attaching to one live session. |
| Session lifecycle | One client can exit without stopping others; final idle removal occurs after the grace period. |
| Creation race | Two concurrent first callers converge on exactly one deterministic container. |
| Cleanup | Nested objects never appear in host Docker; the sentinel survives; Sysbox writes retain host ownership. |
| Host usability | The host can append to a file created by a nested container after cleanup. |

## Files

- `sysbox_linked_worktree_test.go` contains the scenario and domain assertions.
- `sysbox_agents_test.go` covers direct public-launcher behavior and configured local mounts.
- `sysbox_claude_test.go` covers Claude state, managed installation, and coexistence with Codex in one session.
- `sysbox_project_environment_test.go` covers automatic project-image builds and active-session lifecycle.
- `sysbox_fixture_test.go` composes the harness, starts embedded probes, and implements marker synchronization.
- `smoke_setup_test.go` creates the Git fixture, host identity, artifact paths, and launcher processes.
- `docker_harness_test.go` contains all direct host-Docker access and deterministic smoke container names.
- `test_helpers_test.go` contains report, filesystem, Git, mount, and ownership assertions.
- `testdata/environment-probe.sh` observes the interactive environment, identity, sudo, Git config, and tools.
- `testdata/worktree-probe.sh` exercises linked-worktree and mount write behavior.
- `testdata/nested-docker-probe.sh` exercises the private daemon, bind mounts, and Docker Compose.
- `go.mod` and `go.sum` isolate Moby and its transitive dependencies from the application module.

The probe scripts are embedded with `go:embed`; they do not need to be copied into the image or installed on the
host.

## Cleanup and failures

Normal cleanup happens at two levels:

- probes remove their nested container and Compose service when their release marker appears;
- `t.Cleanup` force-removes the deterministic container and host sentinel, closes the Moby client, and lets
  `t.TempDir` remove the Git fixture.

The forced container removal is also the fallback when an assertion stops the scenario before release markers
are written. An abrupt kill of the Go test process can bypass `t.Cleanup`; inspect possible leftovers with:

```bash
docker ps -a --filter label=agents-safe.managed=true
docker ps -a --filter label=agents-safe.smoke=go
```

Container names include the project key, so independent smoke runs do not share a host sentinel or managed
container.

## Extending the scenario

Keep these boundaries when adding coverage:

1. Use Go and the Moby client for host orchestration and host-Docker assertions.
2. Use an embedded shell probe only for behavior that must execute inside the managed container.
3. Report observed values from probes and assert expected values once, in Go.
4. Give every `require` assertion a message that names the broken contract.
5. Preserve early process-exit diagnostics when adding a synchronization barrier.
6. Pin externally pulled workload images by digest.
7. Add smoke-only dependencies to this directory's `go.mod`, not the application module.
8. Keep `make test` as the compile-and-vet gate and `make test-smoke-go` as the real-host gate.

If a new probe holds a client session open, give it an explicit release path so the test can verify both overlapping
clients and final idle removal.

## Scope limits

This test validates the project's intended container configuration and lifecycle on one real host. It does not fuzz
the Docker API, attempt kernel or Sysbox escapes, validate every supported Docker/Sysbox version, or establish a
formal security boundary. Those concerns require separate security review and compatibility testing.
