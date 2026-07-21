# codex-safe

`codex-safe` runs interactive Codex for the current Git project inside an ephemeral container started through
Sysbox. `agents-safe` runs an explicit command in that same environment. Both mount the active checkout at its original
absolute path, start a private Docker daemon inside the container, and reuse a live worktree container instead of
creating another nested Docker environment.

`codex-safe` runs only Codex. Arguments after `--` are forwarded to Codex, not executed as an arbitrary program.
`agents-safe` is the explicit command launcher: for example, `agents-safe bash` starts Bash inside the container.

## What the environment provides

- No-argument `codex-safe` starts interactive Codex for the Git project containing the current directory.
- `agents-safe COMMAND [ARG...]` starts the requested command in the same isolated project environment.
- `codex-safe` requires the resolved host Codex home (`CODEX_HOME`, else `~/.codex`) and may offer to create a missing
  default `~/.codex`; `agents-safe` mounts it only when it already exists. A mounted home is read-write at
  `$HOME/.codex`, and only commands with that mount receive `CODEX_HOME=$HOME/.codex`.
- Personal authored skills under `$HOME/.agents/skills` are mounted read-only when present.
- The pinned Codex CLI comes from an image-owned path a mounted Codex home cannot shadow.
- A regular Git checkout is mounted read-write without exposing other host paths.
- A linked worktree keeps its original absolute path; its primary checkout is read-only while the common `.git`
  directory stays writable.
- The container uses `sysbox-runc` without `--privileged` or the host Docker socket.
- A private nested Docker daemon runs containers and passes the project through at the same absolute path.
- The container uses the invoking host user's login name, primary group name, UID, and GID, and a container-local
  home directory at the same absolute path as host `$HOME`.
- An existing host `$HOME/.gitconfig` is available as a read-only global Git config.
- Interactive tools use a UTF-8 locale and handle Cyrillic input and output; Bash uses a colored prompt.
- Files created by managed commands and nested containers retain ownership that remains usable from the host.
- Concurrent commands for one worktree execute in its already-running container and share its nested daemon.
- A project can add `.agents-safe/Dockerfile` to derive a cached toolchain image from the selected base image.

See the [design document](docs/design-docs/codex-safe.md) for the full product and security model. The
[Codex launch execution plan](docs/exec-plans/completed/2026-07-15-codex-launch-exec-plan.md) records the implementation
scope and validation gates.

## Prerequisites

`codex-safe` requires:

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

## Install

From the repository root:

```bash
make install
```

This installs `codex-safe` and `agents-safe` into `GOBIN`, or into the first `GOPATH/bin` when `GOBIN` is unset,
and builds the local `codex-safe-mvp:local` image. Make sure that Go binary directory is on `PATH`; the launchers can
then be run from any project directory.

For repository-local development builds, use:

```bash
make build
make docker-build
```

The launchers are written to `bin/codex-safe` and `bin/agents-safe`. Run the local checks with:

```bash
make test
```

The image pins Ubuntu 24.04 by digest. It also pins the official `crun` 1.28 binary by SHA-256 for the nested daemon.
The nested runtime preserves the absolute bind-mount contract, including project paths that contain spaces.

The image installs the pinned Codex CLI (`codex-cli 0.144.4`, the `openai/codex` standalone musl release
`rust-v0.144.4`) at the image-owned path `/usr/local/bin/codex`, verified by per-architecture SHA-256 during the
build. Codex is never installed or updated from the network at container startup; updating Codex requires a new image
build with a re-approved version and digest per [`docs/dependencies.md`](docs/dependencies.md).

The environment includes Git, Docker Engine and CLI with Buildx/BuildKit, Docker Compose V2, `sudo`, `make`, `less`,
and `rg`.
The recreated host user has passwordless `sudo` for container-local administration such as `sudo apt-get update`.
Interactive Bash sessions also load the system completion framework, including Make target completion.
The image defaults to `C.UTF-8` and `TERM=xterm-256color`, so Bash and text tools handle Cyrillic and terminal colors.
The project Bash startup file enables a colored prompt plus automatic colors for `ls` and `grep`.

The launcher creates an otherwise empty container-local home at the same absolute path as host `$HOME`; it does not
mount the host home directory. If host `$HOME/.gitconfig` exists, the launcher mounts that file read-only at the same
path inside the container. Files referenced by `include.path` are available only when independently present in an
allowed mount.

## Run Codex

The product interface is:

```text
codex-safe [--project PATH] [--image REF] [-- CODEX ARG...]
```

Start interactive Codex for the current Git project:

```bash
./bin/codex-safe
```

Arguments after `--` are forwarded to Codex as separate arguments; the launcher never runs another executable. For
example, run a non-interactive Codex turn:

```bash
./bin/codex-safe --project . -- exec "summarize the build failure"
```

While Codex runs, another terminal reuses the same container for the same worktree. Each project/UID pair maps
to one deterministic `codex-safe-<24-hex-key>` container name. The launcher inspects that exact name, validates the
ownership and manager-protocol labels, and compares the `codex-safe.launch-config` creation fingerprint before
reuse. Every invocation, including the first, runs
`docker exec codex-safe-session run -- /usr/local/bin/codex [CODEX ARG...]`; no user command owns the container
lifecycle.

A relaunch that resolves a different Codex home or personal-skills source for a still-running worktree is rejected
with a finish-active-session diagnostic: user mounts are fixed when the container is created, so the launcher
neither reuses the stale session nor terminates the live one.

When stdin and stdout are attached to a terminal, the launcher allocates a Docker TTY and forwards terminal input, so
interactive Codex behaves as it does on the host.

## Run a command

The generic command interface is:

```text
agents-safe init [--project PATH] [--host-caches=auto|none|go_build,go_modules,uv]
agents-safe [--project PATH] [--image REF] [--] COMMAND [ARG...]
```

Prepare an inactive project-environment template from anywhere inside a Git worktree:

```bash
./bin/agents-safe init --host-caches=auto
```

This creates `.agents-safe/Dockerfile.sample`, `.agents-safe/config.toml`, and `.agents-safe/.gitignore` without
contacting Docker. The local ignore file ignores generated project-environment files while keeping itself and an
activated `.agents-safe/Dockerfile` trackable; the worktree-root `.gitignore` is not modified. Repeated
initialization preserves existing content.

Host-backed cache initialization snapshots existing `GOCACHE`, `GOMODCACHE`, and project-effective uv cache
directories into a newly created config. The launcher mounts each at its configured host-visible path and supplies
`GOCACHE`, `GOMODCACHE`, or `UV_CACHE_DIR` to every managed command. uv must be provided by the project image; the
base runtime image stays uv- and Python-free. Initialization never creates cache directories or rediscover/rewrite an
existing config. Maven, Gradle, Docker-image, and BuildKit caches are deferred; see
[Host-Backed Dependency Caches](docs/design-docs/host-backed-dependency-caches.md).

Edit the Dockerfile sample, then rename it to `.agents-safe/Dockerfile` to activate automatic project-image builds.
Append an `additional` entry to the generated mount list when the project container needs another absolute host
directory:

```toml
[[common.mounts]]
role = "additional"
source = "/home/user/.cache/example-tool"
target = "/home/user/.cache/example-tool"
read_only = false
comment = "Expose the example-tool cache to the project environment."
```

Configured directories are mounted read-write at the same absolute paths for new containers. They must exist and
must not overlap the project or one another. The file intentionally expands host access and can be changed by code in
the writable worktree, so review it before a cold launch. Use `./bin/agents-safe -- init` when `init` is the container
command you intend to execute.

For example, open Bash inside the environment for the current Git project:

```bash
./bin/agents-safe bash
```

Options must precede `COMMAND`. The optional `--` marks the end of launcher options; it is useful when the command
name starts with a hyphen. `agents-safe` sends the command and arguments directly to the container session wrapper,
without invoking a host shell. The command can use programs installed in the image, such as Bash, Git, Make, Docker,
and `rg`, or executables available under the mounted project. It receives the same project, user mounts,
identity, nested Docker daemon, working directory, and lifecycle behavior as `codex-safe`.

### Codex home, personal skills, and authentication

- The launcher resolves the Codex home from host `CODEX_HOME`, or `~/.codex` below the operating-system home, and
  mounts that canonical directory read-write at `$HOME/.codex`. A missing, relative, root, non-directory, or
  inaccessible source fails preflight; the launcher never creates it and never falls back.
- Personal authored skills under `$HOME/.agents/skills` are mounted read-only when present. A skills source that
  overlaps a writable mount (worktree, common Git directory, or Codex home), including through a symlink, is rejected
  so a read-only skill cannot be modified through a writable alias.
- Codex reads and persists its configuration, sessions, logs, and skills through the mounted Codex home. File-based
  credentials in `$CODEX_HOME/auth.json` are available; credentials stored only in a host keychain or keyring are not
  mounted. An interactive `codex login` inside the container persists file-based credentials to the mounted home.

The selected project path must be inside a non-bare Git working tree. Linked worktrees are supported when their
common Git directory is the `.git` directory of an existing primary checkout. External common Git directories and
worktrees attached to bare repositories are rejected.

### Project-specific environments

Place a Dockerfile at the worktree root:

```text
.agents-safe/Dockerfile
```

The Dockerfile itself is consent to build it with the host Docker daemon. When a new session is needed, the launcher
uses only `.agents-safe/` as the build context and rebuilds one stable per-project tag. Docker/BuildKit decides whether
to reuse cached layers; the launcher computes no parallel context digest and asks no additional confirmation.

The derived image must retain the session entrypoint, `serve` command, private Docker socket configuration, and
required runtime binaries. A failed build or validation stops the launch; the launcher never falls back to the base
image.

`--image REF` takes precedence over project-image discovery. It intentionally bypasses `.agents-safe/Dockerfile`
validation and image building, including when `REF` equals the normal default. A running session retains its selected
environment until its active commands finish, even if the Dockerfile changes or is removed. The next cold launch uses
the then-current project definition.

## Run the real-host smoke test

The full Sysbox smoke scenario is an opt-in Go test. It uses the Docker Engine client for host-side container creation
and inspection, while focused embedded workloads exercise the environment, linked worktree, nested Docker, and Codex
integration inside the isolated container:

```bash
make test-smoke-go
```

It is intentionally separate from `make test`: the Go test requires a real Sysbox host and a Docker image build. The
lifecycle probes run arbitrary bash through the public `agents-safe` command, and the Codex-specific assertions run
`codex-safe` directly, so the suite exercises the same product binaries a user runs.

The harness builds the binaries and image, creates a temporary primary repository and linked worktree, starts a host
sentinel container, and performs live assertions against the Sysbox container and nested containers. It verifies
account names, global Git config, UTF-8 text, mount modes, Git writes, daemon separation, overlapping command lifetime,
deterministic container reuse, idle removal, concurrent first callers, nested project access, file ownership, and
cleanup. The Codex scenario also proves Codex-home state round-trip with host ownership, read-only personal skills, an
unavailable external symlink target, image-owned-executable shadowing rejection, and the user-mount reuse-mismatch
diagnostic.

See [the smoke-test README](tests/smoke/README.md) for the architecture, synchronization protocol, complete assertion
catalog, cleanup behavior, and extension guidelines.

The smoke suite was run successfully on 2026-07-15 with Docker Engine 28.3.3, Sysbox in the registered runtime set,
and the pinned Codex CLI. Other kernel, filesystem, and Sysbox combinations must pass the same test before use.

## Security boundary and omissions

The project and linked-worktree common Git directory are writable by the agent. Code in the environment can change or
delete them, and can read or change the mounted Codex home and its credentials. The primary checkout is read-only,
except for its separately mounted common `.git` directory. Personal skills are read-only, but scripts they contain
execute with the agent's permissions when Codex selects them.

The container is deliberately started without `--privileged`, host namespaces, or `/var/run/docker.sock`.
Nested containers are controlled by a daemon whose socket and storage exist only inside that container. This is
an infrastructure isolation check, not protection against kernel, Docker, Sysbox, image, or runtime vulnerabilities.
Passwordless `sudo` grants root inside the container, not host root. It can modify the ephemeral image and all
host paths already mounted read-write, but it does not add host mounts or expose the host Docker socket.

This release intentionally omits:

- CPU, memory, PID, and nested-storage limits;
- persistent nested Docker cache and port publication;
- network egress restrictions and secret-exfiltration controls;
- reuse of credentials stored only in a host keychain or keyring;
- rootless or remote host Docker, Docker Desktop, macOS, and Windows;
- production image publication, signing, and packaging.

Each container session uses fresh nested Docker storage. While any wrapped foreground command is running, later
invocations for the same canonical worktree and host UID execute in that container and reuse its storage. After the
last command exits, the manager waits five seconds, stops the nested daemon, and Docker removes the container
through `--rm`; stopped containers are not resumed. If the launcher or host daemon is killed abruptly, inspect
project-owned sessions with:

```bash
docker ps -a --filter label=codex-safe.managed=true
```

Review a candidate carefully before removing it; codex-safe does not yet provide a stale-session cleanup command.
