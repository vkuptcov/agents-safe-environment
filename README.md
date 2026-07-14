# codex-safe infrastructure MVP

This repository contains a small Go launcher that tests the infrastructure needed for a safer local agent
environment. It starts an ephemeral outer container through Sysbox, mounts one Git checkout at its original absolute
path, and starts a private Docker daemon inside the container. A later invocation for the same live worktree reuses
that container instead of creating another nested Docker environment.

The current command is a probe runner, not the finished Codex launcher. It does not install Codex or mount
`~/.codex`.

## What the MVP proves

- A regular Git checkout can be mounted read-write without exposing other host paths.
- A linked worktree can keep its original absolute path and its Git metadata can remain usable.
- The primary checkout of a linked worktree can be read-only while the common `.git` directory stays writable.
- The outer container can use `sysbox-runc` without `--privileged` or the host Docker socket.
- A private nested Docker daemon can run containers and pass the project through at the same absolute path.
- The probe uses the invoking host user's login name, primary group name, UID, and GID.
- The probe's container-local home directory uses the same absolute path as host `$HOME`.
- An existing host `$HOME/.gitconfig` is available as the probe's read-only global Git config.
- Interactive tools use a UTF-8 locale and handle Cyrillic input and output.
- Interactive Bash sessions use a colored prompt and color-aware command defaults.
- Files created by the probe and nested containers retain ownership that remains usable from the host.
- Concurrent commands for one worktree execute in its already-running outer container and share its nested daemon.

See the [design document](docs/design-docs/codex-safe.md) for the intended product and security model. The
[MVP execution plan](docs/exec-plans/review/2026-07-13-codex-safe-mvp-exec-plan.md) records the implementation scope
and validation gates.

## Prerequisites

The MVP requires:

- a Linux host on `amd64` or `arm64`;
- Go 1.26 or newer;
- Git and Bash;
- a local Docker Engine available to the invoking user;
- `sysbox-runc` registered with that Docker Engine;
- registry and GitHub access while the image and nested fixture are first downloaded.

Confirm Sysbox registration before building:

```bash
docker info --format '{{json .Runtimes}}'
```

The output must contain `sysbox-runc`. Runtime registration alone is not enough to establish that the host kernel,
filesystem, and Sysbox installation support this workload; run the smoke test below for that proof.

## Build

From the repository root:

```bash
make build
make docker-build
```

The launcher is written to `bin/codex-safe`. Run the local checks with:

```bash
make test
```

The image pins Ubuntu 24.04 by digest. It also pins the official `crun` 1.28 binary by SHA-256 for the nested daemon.
The nested runtime preserves the absolute bind-mount contract, including project paths that contain spaces.

The environment includes Git, Docker Engine and CLI, Docker Compose V2, `sudo`, `make`, `less`, and `rg`.
The recreated host user has passwordless `sudo` for container-local administration such as `sudo apt-get update`.
Interactive Bash sessions also load the system completion framework, including Make target completion.
The image defaults to `C.UTF-8` and `TERM=xterm-256color`, so Bash and text tools handle Cyrillic and terminal colors.
The project Bash startup file enables a colored prompt plus automatic colors for `ls` and `grep`.

The launcher creates an otherwise empty container-local home at the same absolute path as host `$HOME`; it does not
mount the host home directory. If host `$HOME/.gitconfig` exists, the launcher mounts that file read-only at the same
path inside the container. Files referenced by `include.path` are available only when independently present in an
allowed mount.

## Run a probe

The MVP interface is:

```text
codex-safe [--project PATH] [--image REF] -- COMMAND [ARG...]
```

Start an interactive shell in the current project:

```bash
./bin/codex-safe -- bash
```

Inside that shell, Compose uses the private nested Docker daemon:

```bash
docker compose up -d
docker compose ps
```

While that shell remains open, another terminal can run a command in the same outer container:

```bash
./bin/codex-safe --project . -- make test
```

New outer containers carry the canonical worktree root in `codex-safe.project-path` and the invoking UID in
`codex-safe.host-uid`. The launcher searches running containers by both labels. One match is reused with `docker exec`;
multiple matches are rejected as ambiguous. The repeated invocation may select a different directory inside the same
worktree, and that directory becomes the exec working directory.

When stdin and stdout are attached to a terminal, the launcher allocates a Docker TTY and forwards terminal input.
For pipelines and redirected output it keeps stdin attached without forcing a TTY:

```bash
printf 'git status --short\n' | ./bin/codex-safe -- bash
```

For example, run Git and inspect the private Docker daemon from the current project:

```bash
./bin/codex-safe --project . --image codex-safe-mvp:local -- \
    bash -lc 'git status --short && docker info --format "{{.ID}} {{.DefaultRuntime}}"'
```

Arguments after `--` are passed as separate process arguments. The launcher does not evaluate them through a host
shell. A shell used explicitly in the probe, as in the example, still interprets its own command string inside the
outer container.

The selected project path must be inside a non-bare Git working tree. Linked worktrees are supported when their
common Git directory is the `.git` directory of an existing primary checkout. External common Git directories and
worktrees attached to bare repositories are rejected.

## Run the real-host smoke test

```bash
bash tests/smoke/sysbox-linked-worktree.sh
```

The harness builds the Go binary and image, creates a temporary primary repository and linked worktree, starts a host
sentinel container, and performs live assertions against the outer and nested containers. It verifies account names,
global Git config, UTF-8 text, mount modes, Git writes, daemon separation, same-container command reuse, nested project
access, file ownership, and cleanup.

The smoke test was run successfully on 2026-07-13 with Docker Engine 28.3.3, Sysbox CE 0.7.0, cgroup v2, and the
`overlay2` storage driver. Other kernel, filesystem, and Sysbox combinations must pass the same test before use.

## Security boundary and MVP omissions

The project and linked-worktree common Git directory are writable by the probe. Code in the environment can change or
delete them. The primary checkout is read-only, except for its separately mounted common `.git` directory.

The outer container is deliberately started without `--privileged`, host namespaces, or `/var/run/docker.sock`.
Nested containers are controlled by a daemon whose socket and storage exist only inside that outer container. This is
an infrastructure isolation check, not protection against kernel, Docker, Sysbox, image, or runtime vulnerabilities.
Passwordless `sudo` grants root inside the outer container, not host root. It can modify the ephemeral image and all
host paths already mounted read-write, but it does not add host mounts or expose the host Docker socket.

This MVP intentionally omits:

- Codex installation and authentication;
- the `~/.codex` mount described by the product design;
- CPU, memory, PID, and nested-storage limits;
- persistent nested Docker cache and port publication;
- network egress restrictions and secret-exfiltration controls;
- rootless or remote host Docker, Docker Desktop, macOS, and Windows;
- production image publication, signing, update policy, and packaging.

Each outer-container session uses fresh nested Docker storage. While its main command is running, later invocations for
the same canonical worktree and host UID execute in that container and reuse its storage. Normal exit of the main
command still removes the outer container through Docker `--rm`; stopped containers are not resumed. If the launcher
or host daemon is killed abruptly, inspect project-owned sessions with:

```bash
docker ps -a --filter label=codex-safe.session
```

Review a candidate carefully before removing it; the MVP does not yet provide a stale-session cleanup command.
