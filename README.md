# Save Development Environment for Agents

Run Codex, Claude Code, or ordinary development commands in an isolated Docker environment for the Git project you
are already in. Install once; normal projects need no configuration.

```bash
codex-safe                 # open Codex
claude-safe                # open Claude Code
agents-safe bash           # open a shell
agents-safe make test      # run a project command
```

That is the normal workflow. The first command creates a project container; later commands reuse it while it is
active. Your checkout remains at the same path and is writable, so the tools work on your real project without a
copy, VM, or manual `docker run` command.

## Why use it?

`agents-safe` gives every Git worktree a short-lived Linux development environment with the tools that agents usually
need: Git, Docker and Compose, Buildx, Make, Bash, `rg`, `jq`, `sqlite3`, `curl`, and diagnostics tools. Nested
containers use a private Docker daemon inside the environment.

You can run `docker`, `docker compose`, and builds there as usual. They talk only to that private daemon: its images,
containers, volumes, and socket are separate from the host Docker daemon and disappear with the project session.
Published ports do not conflict with the host or another project session, so two projects can both use same ports, e.g. `-p 8080:8080`.
It helps to simultaneously test the same project in different worktrees.
Containers inside the same project session still share one private daemon and must use distinct published ports.

It is deliberately isolated from the host:

- no host Docker socket;
- no privileged container or shared host namespaces;
- no implicit host-home mount;
- files created in the checkout remain owned by a usable host identity.

Codex and Claude Code share the project container when used for the same worktree, but keep their own host-backed
state. By default, the launcher detects and connects available host Git configuration, Codex state, Claude Code state,
personal skills, and eligible host MCP servers. Explicit flags or `.agents-safe/config.toml` override that resolved
default; an unavailable optional integration stays absent and produces a warning where relevant. When the last command
exits, the environment is removed after a short idle delay. See [Architecture](ARCHITECTURE.md) for the full topology
and security boundary.

It never auto-mounts `~/.ssh`, the host home directory, keychains, or arbitrary secret directories. Dependency caches
are separate: `agents-safe init` detects existing Go build (`GOCACHE`), Go module (`GOMODCACHE`), and uv caches by
default and records them in local `config.toml`; use `--host-caches=none` or an explicit cache list to override this.

## Start in three steps

### 1. Meet the prerequisites

Install the following on a Linux `amd64` or `arm64` host:

- [Go](https://go.dev/dl/) 1.26 or newer;
- [Docker Engine](https://docs.docker.com/engine/install/), usable by your user;
- [Sysbox](https://github.com/nestybox/sysbox), registered as Docker's `sysbox-runc` runtime;
- [Git](https://git-scm.com/downloads) and [Bash](https://www.gnu.org/software/bash/).

Confirm the Sysbox runtime is registered:

```bash
docker info --format '{{json .Runtimes}}' | grep 'sysbox-runc'
```

The output must contain `sysbox-runc`. The first image build also downloads the image and the current Linux Codex and
Claude Code releases, so it needs registry and GitHub access.

### 2. Install the launchers

Clone this repository and run:

```bash
make install
```

This installs `codex-safe`, `claude-safe`, and `agents-safe` into `GOBIN`, or the `bin` directory of the first
`GOPATH` entry when `GOBIN` is unset. It also builds the local image and initializes the two agent installations. Put
the install directory on `PATH` once; then use the commands from any Git project.

### 3. Use it from a Git project

Change into the project or any directory below it, then choose what you want to run:

```bash
cd /path/to/your/git-project

codex-safe
claude-safe
agents-safe bash
agents-safe make test
```

No project configuration is required for these commands. They discover the enclosing Git worktree automatically.
Use `--project PATH` if you want to choose a different worktree:

```bash
codex-safe --project /path/to/your/git-project
agents-safe --project /path/to/your/git-project make test
```

## One project, one shared environment

`codex-safe`, `claude-safe`, and `agents-safe` use the same active container when they target the same Git worktree
and host user. They see the same checkout, processes, and private Docker daemon.

For example, start Codex in one terminal:

```bash
cd /path/to/your/git-project
codex-safe
```

While it is still running, open a second terminal in the same project and enter that container:

```bash
cd /path/to/your/git-project
agents-safe bash
```

From that shell you can inspect what is happening:

```bash
ps aux
docker ps
git status
```

You can also inspect without opening a shell:

```bash
agents-safe ps aux
agents-safe docker ps
```

Starting `claude-safe` from the same worktree attaches Claude Code to that environment too. Every invocation has its
own foreground command, but they share the container until the last command exits and the idle timeout removes it.
Use the same `--project PATH` on each command when invoking them from outside the worktree.

## Use the agents

### Codex

Start an interactive session:

```bash
codex-safe
```

Pass Codex arguments after `--`:

```bash
codex-safe -- exec "summarize the build failure"
```

`codex-safe update` refreshes the shared Linux Codex installation without starting a project session.

Codex state comes from the host `CODEX_HOME`, or `~/.codex` by default. An existing source is mounted at the stable
container path `$HOME/.codex`, which keeps your login, configuration, sessions, and file-based credentials available.
If an explicit `CODEX_HOME` is missing or invalid, startup fails before Docker starts. If only the implicit
`~/.codex` is absent, `codex-safe` warns and uses ephemeral state.

### Claude Code

Start an interactive session:

```bash
claude-safe
```

Pass Claude Code arguments after `--`:

```bash
claude-safe -- --permission-mode plan
```

`claude-safe update` refreshes the independent shared Linux Claude Code installation. Default host state uses
`~/.claude` and `~/.claude.json` when both exist; set `CLAUDE_CONFIG_DIR` to use another Claude state directory.

### Any command

Use `agents-safe` for a shell, a test suite, Docker Compose, or any executable available in the image or your
project:

```bash
agents-safe bash
agents-safe make test
agents-safe docker compose up --build
agents-safe ./scripts/check.sh
```

Options belong before the command. Use `--` only when you need to mark the end of launcher options:

```bash
agents-safe -- bash
```

## Configure a project only when it needs more

Most projects need no setup beyond the commands above. If yours needs an extra toolchain or explicit host cache
mounts, initialize a local template once from inside its worktree:

```bash
agents-safe init
```

The command does not start Docker or overwrite existing content. Its default `--host-caches=auto` mode records any
existing Go and uv caches; use `--host-caches=none` when you want no host dependency caches.

| Generated file | Purpose | Commit it? |
| --- | --- | --- |
| `.agents-safe/Dockerfile.sample` | Starting point for project tools. | No; rename it first. |
| `.agents-safe/config.toml` | Local image, mounts, caches, and launcher defaults. | No; it contains host paths. |
| `.agents-safe/.gitignore` | Keeps local config ignored and an activated Dockerfile trackable. | Yes. |

Initialization does not change the project-root `.gitignore`. Review the host-specific `config.toml` before a cold
launch because every additional writable mount expands what project code can change on the host.

### Add your project dependencies

Edit the sample, then rename it to activate it:

```bash
mv .agents-safe/Dockerfile.sample .agents-safe/Dockerfile
```

For example, add a system tool while retaining the launcher-selected base environment:

```dockerfile
ARG AGENTS_SAFE_BASE=agents-safe-mvp:local
FROM ${AGENTS_SAFE_BASE}

RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
```

On the next new session, `agents-safe` builds this image using only `.agents-safe/` as the build context. A failed
build stops the launch; it never silently falls back to the base image. Commit `.agents-safe/Dockerfile` and
`.agents-safe/.gitignore` to give the project the same tool definition on every host.

Use `--image REF` when you want an already-built image for one invocation. An explicit image bypasses automatic
`.agents-safe/Dockerfile` discovery and building.

### Add a host directory deliberately

The worktree is already mounted. To expose one more host directory, add it to `.agents-safe/config.toml`:

```toml
[[common.mounts]]
role = "additional"
source = "/home/user/.cache/example-tool"
target = "/home/user/.cache/example-tool"
read_only = false
comment = "Expose the example-tool cache to the project environment."
```

The directory must already exist and cannot overlap the project or another configured mount. This is an explicit
expansion of host access, so do not add broad directories such as your home.

By default, detected project Python virtual environments are masked, preventing image changes from affecting a host
`.venv`. Set `use_host_python_venv = true` in the config, or pass `--use-host-python-venv`, only when you deliberately
want to use it.

By default the session container is removed when it stops after the idle timeout. Set `keep_container = true` in the
config, or pass `--keep-container`, to keep it: the next launch restarts the stopped container instead of creating a
new one, so nested Docker images, installed packages, and other changes to the container filesystem survive. Masked
`.venv` directories still live on tmpfs and are recreated on every start. A kept container stays until you remove it
with `docker rm`; the launcher prints a notice whenever it restarts one.

For all config fields and precedence rules, see
[Project launcher configuration](docs/design-docs/project-launcher-configuration.md).

## What is shared, and what is not

- One active container is reused per Git worktree and host user. Codex, Claude Code, and `agents-safe` commands can
  run concurrently in it.
- The worktree and its Git metadata are writable. A linked worktree is supported; its primary checkout is read-only.
- Codex and Claude state are separate writable mounts. They are persistence stores, not isolation boundaries: every
  process in the shared container can read mounted agent state and file-based credentials allowed by host file modes.
- Personal skills in `~/.agents/skills`, when present, are mounted read-only.
- The host home, Docker socket, and host namespaces are not mounted. Host keychain-only credentials are not
  available.
- Nested Docker images, containers, and storage live only for the active session unless `keep_container` is set,
  in which case they persist in the stopped container between sessions.

An active environment has a fixed creation contract. If you change its image or mount setup while it is running,
finish the active commands and start again. `--force-exec` is an emergency way to run a command in the compatible
existing environment; it does not update that environment's mounts or image.

## Verify the host

After installing on a new Linux/Sysbox host, run the real boundary test from this repository checkout:

```bash
make test-smoke-go
```

It is intentionally separate from normal unit tests and proves the Sysbox, mount, linked-worktree, nested-Docker,
identity, lifecycle, and agent-installation behavior against the real host. See
[the smoke-test guide](tests/smoke/README.md) for requirements and cleanup behavior.

## Limits

This release currently requires Linux with local Docker and Sysbox. It does not support Docker Desktop, macOS,
Windows, rootless or remote Docker. It also does not impose resource limits, restrict network egress, or persist
nested Docker storage. Read [Architecture](ARCHITECTURE.md) and
[Security](docs/security.md) before relying on it for a sensitive workload.

## Develop this repository

```bash
make build
make docker-build
make test
make check-docs
```

The complete command catalog is in [Makefile reference](docs/makefile-reference.md). Design and operational details
are routed from [the documentation index](docs/index.md).
