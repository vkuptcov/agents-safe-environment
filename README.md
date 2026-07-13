# codex-safe infrastructure MVP

This repository contains a small Go launcher that tests the infrastructure needed for a safer local agent
environment. It starts an ephemeral outer container through Sysbox, mounts one Git checkout at its original absolute
path, and starts a private Docker daemon inside the container.

The current command is a probe runner, not the finished Codex launcher. It does not install Codex or mount
`~/.codex`.

## What the MVP proves

- A regular Git checkout can be mounted read-write without exposing other host paths.
- A linked worktree can keep its original absolute path and its Git metadata can remain usable.
- The primary checkout of a linked worktree can be read-only while the common `.git` directory stays writable.
- The outer container can use `sysbox-runc` without `--privileged` or the host Docker socket.
- A private nested Docker daemon can run containers and pass the project through at the same absolute path.
- Files created by the probe and nested containers can retain the invoking host user's UID and GID.

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
go build -o /tmp/codex-safe ./cmd/codex-safe
docker build -t codex-safe-mvp:local -f container/Dockerfile .
```

The image pins Ubuntu 24.04 by digest. It also pins the official `crun` 1.28 binary by SHA-256 for the nested daemon.
The nested runtime preserves the absolute bind-mount contract, including project paths that contain spaces.

## Run a probe

The MVP interface is:

```text
codex-safe [--project PATH] [--image REF] -- COMMAND [ARG...]
```

For example, run Git and inspect the private Docker daemon from the current project:

```bash
/tmp/codex-safe --project . --image codex-safe-mvp:local -- \
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
sentinel container, and performs live assertions against the outer and nested containers. It verifies mount modes,
Git writes, daemon separation, nested project access, file ownership, and cleanup.

The smoke test was run successfully on 2026-07-13 with Docker Engine 28.3.3, Sysbox CE 0.7.0, cgroup v2, and the
`overlay2` storage driver. Other kernel, filesystem, and Sysbox combinations must pass the same test before use.

## Security boundary and MVP omissions

The project and linked-worktree common Git directory are writable by the probe. Code in the environment can change or
delete them. The primary checkout is read-only, except for its separately mounted common `.git` directory.

The outer container is deliberately started without `--privileged`, host namespaces, or `/var/run/docker.sock`.
Nested containers are controlled by a daemon whose socket and storage exist only inside that outer container. This is
an infrastructure isolation check, not protection against kernel, Docker, Sysbox, image, or runtime vulnerabilities.

This MVP intentionally omits:

- Codex installation, authentication, and interactive terminal integration;
- the `~/.codex` mount described by the product design;
- CPU, memory, PID, and nested-storage limits;
- persistent nested Docker cache and port publication;
- network egress restrictions and secret-exfiltration controls;
- rootless or remote host Docker, Docker Desktop, macOS, and Windows;
- production image publication, signing, update policy, and packaging.

Each launch uses fresh nested Docker storage. Normal exit removes the outer container through Docker `--rm`. If the
launcher or host daemon is killed abruptly, inspect project-owned sessions with:

```bash
docker ps -a --filter label=codex-safe.session
```

Review a candidate carefully before removing it; the MVP does not yet provide a stale-session cleanup command.
