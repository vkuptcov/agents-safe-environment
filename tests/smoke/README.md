# Sysbox smoke tests

This directory contains the real-host integration test for `codex-safe`. The test launches `codex-safe-probe`, a
test-only transport compiled only under the `smoke` build tag, which drives the same launcher create, reuse, and
exec path with an arbitrary command. The product `codex-safe` binary runs only Codex and carries no
arbitrary-command surface, so the probe preserves lifecycle coverage without reintroducing one. The test creates
an outer container with the `sysbox-runc` runtime, starts a private Docker daemon inside that container, and
exercises a linked Git worktree through multiple concurrent client commands.

The smoke test checks the boundaries between the host, the managed Sysbox container, and containers started by
the nested Docker daemon. It complements unit tests; it is not a replacement for them or a general proof that
the sandbox cannot be escaped.

## Quick start

Run the complete scenario from the repository root:

```bash
make test-smoke-go
```

The target:

1. builds `bin/codex-safe`, `bin/codex-safe-session`, and the smoke-tagged `bin/codex-safe-probe`;
2. builds the `codex-safe-mvp:local` image;
3. enables the opt-in smoke test with `CODEX_SAFE_RUN_SYSBOX_SMOKE=1`;
4. runs every `TestSysbox` scenario (linked worktree and Codex product launch) without the Go test cache.

The test requires:

- a Linux host;
- a reachable Docker Engine daemon;
- `sysbox-runc` registered as a Docker runtime;
- Git and Go 1.26 or newer on the host;
- registry access to pull uncached Dockerfile inputs and the pinned nested workload image.

Check the registered Docker runtimes with:

```bash
docker info --format '{{json .Runtimes}}'
```

For a direct invocation, build the prerequisites first and run the test from its own Go module:

```bash
make build build-smoke-probe docker-build
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
├── launcherHarness ── starts bin/codex-safe-probe as a real host process
├── dockerHarness ──── inspects the host daemon through the Moby client
├── temporary Git primary checkout + linked worktree
└── host sentinel container
             │
             │ codex-safe-probe --project <linked worktree> -- <command>
             ▼
Managed outer container (sysbox-runc, not privileged)
├── codex-safe-session manager
├── recreated host user, group, and home path
├── mounted project, common Git directory, and read-only .gitconfig
└── private Docker daemon using crun
             │
             ├── docker run container
             └── Docker Compose service
```

The Go test accesses the host Docker socket. The socket is deliberately not mounted into the outer container.
Commands in the outer container talk to the private nested daemon instead.

## Scenario

`TestSysboxLinkedWorktreeGo` runs one ordered end-to-end scenario:

1. Create a temporary primary Git checkout, a linked worktree, a nested project directory, and a synthetic host
   home. Paths contain spaces so path quoting is exercised.
2. Create a host sentinel container. The nested daemon must never be able to see it.
3. Start the environment probe from the nested project directory. This first client creates the deterministic
   outer container and remains connected at a synchronization barrier.
4. Run the worktree probe through a second client. It must reuse the outer container, modify and stage a linked
   worktree file, write the common Git directory, and fail to modify the read-only primary checkout.
5. Start a nested-Docker probe through another client. It runs a container with a project bind mount and starts
   a Compose service on the private daemon.
6. Inspect the live outer container from the host and validate its labels, runtime, working directory, privilege
   mode, and mounts.
7. Start another command and prove that it sees the same outer hostname and nested daemon. Release clients one at
   a time and prove that the remaining clients and outer container stay alive.
8. Release the final client, observe the idle grace period, wait for automatic outer-container removal, and
   validate nested cleanup and host-side file ownership.
9. Start two first callers concurrently against a fresh session and prove that exactly one deterministic outer
   container is created.

## Probe synchronization

Long-running probes communicate through files in the temporary linked worktree. That directory is visible to both
the host test and commands inside the outer container.

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
| Worktree | Git works from a linked worktree; a file can be staged; the common Git directory is writable. |
| Mount policy | The primary checkout is read-only; the linked worktree and common Git directory are writable. |
| Mount isolation | Mounts use `rprivate`; no source or destination is the host Docker socket. |
| Outer container | It uses `sysbox-runc`, is not privileged, preserves its working directory, and has exact labels. |
| Nested daemon | Its ID differs from the host daemon, it uses `crun`, and it cannot see the sentinel. |
| Nested workloads | `docker run` writes through a bind mount and the Compose service reaches the running state. |
| Session reuse | Overlapping clients share one outer hostname, one nested daemon, and one managed container. |
| Session lifecycle | One client can exit without stopping others; final idle removal occurs after the grace period. |
| Creation race | Two concurrent first callers converge on exactly one deterministic outer container. |
| Cleanup | Nested objects never appear in host Docker; the sentinel survives; Sysbox writes retain host ownership. |
| Host usability | The host can append to a file created by a nested container after cleanup. |

## Files

- `sysbox_linked_worktree_test.go` contains the scenario and domain assertions.
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
- `t.Cleanup` force-removes the deterministic outer container and host sentinel, closes the Moby client, and lets
  `t.TempDir` remove the Git fixture.

The forced outer-container removal is also the fallback when an assertion stops the scenario before release markers
are written. An abrupt kill of the Go test process can bypass `t.Cleanup`; inspect possible leftovers with:

```bash
docker ps -a --filter label=codex-safe.managed=true
docker ps -a --filter label=codex-safe.smoke=go
```

Container names include the project key, so independent smoke runs do not share a host sentinel or managed outer
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
