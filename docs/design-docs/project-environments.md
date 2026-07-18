# Project-Specific Agent Environments

Status: Implemented

Decision: the owner accepted Dockerfile-as-consent and BuildKit-owned cache semantics on 2026-07-18.

Scope:

- project-owned system toolchains and packages needed inside `codex-safe` and `agents-safe` sessions;
- initialization of an inactive `.agents-safe/Dockerfile.sample` template;
- automatic host-side builds from `.agents-safe/Dockerfile`;
- local read-write mounts declared in `.agents-safe/config.toml`;
- stable project-image naming, static compatibility checks, and active-session reuse;
- precedence of the existing explicit `--image` override.

## Purpose and Intent

Different projects need different toolchains. Installing every tool in the shared image makes it large and cannot
support conflicting versions. Installing tools manually in a running session is also temporary because the session
container is removed after its final command and idle timeout.

A project can instead add one file:

```dockerfile
ARG AGENTS_SAFE_BASE
FROM ${AGENTS_SAFE_BASE}

RUN apt-get update \
    && apt-get install --yes --no-install-recommends openjdk-21-jdk-headless \
    && rm -rf /var/lib/apt/lists/*
```

Both launchers discover that file, build a derived image when a new session is needed, and start the ordinary Sysbox
session from the immutable build result. The project owns tool and version selection; the launcher owns only the base
image argument, fixed context, stable tag, and runtime compatibility boundary.

The presence of `.agents-safe/Dockerfile` is explicit consent to execute its build through the host Docker daemon.
There is no additional launcher prompt or trust database.

## Success Criteria

- A project adds one Dockerfile and continues to use ordinary `codex-safe` or `agents-safe` commands.
- Every cold create invokes `docker build`; Docker/BuildKit decides which layers need rebuilding.
- Two projects can use different toolchain versions without sharing writable SDK state.
- The build context exposes no project or host files outside `.agents-safe/`.
- A derived image preserves the session manager, Codex CLI, nested Docker, and root-bootstrap contracts.
- Changing or removing the Dockerfile never mutates an active session.
- Build or compatibility failure stops the launch without falling back to the base image.

## Contract

### 1. Project initialization

`agents-safe init [--project PATH]` discovers the selected Git worktree and creates:

```text
<canonical-worktree-root>/.agents-safe/Dockerfile.sample
<canonical-worktree-root>/.agents-safe/config.toml
```

Initialization is a host-side filesystem operation. It does not initialize Docker, inspect an image, build a project
environment, or start a session. Invocation from a nested directory still writes to that worktree's root.

The command creates `.agents-safe/` when absent, rejects that path when it is a symlink or non-directory, and never
overwrites either generated file. Existing sibling files, including an active `Dockerfile`, are preserved. Repeated
initialization creates only missing outputs.

The sample is deliberately inactive. The user reviews and edits it, then renames it to `Dockerfile` to opt into the
host-side build. A leading `init` selects this host subcommand; `agents-safe -- init` still launches a container
command named `init`.

The root `.gitignore` receives exact rules for `/.agents-safe/Dockerfile.sample` and
`/.agents-safe/config.toml`. The command preserves all existing rules and does not ignore `.agents-safe/` or its
active `Dockerfile`.

### 2. Local mount configuration

The generated configuration starts with an empty list:

```toml
# Additional host directories mounted read-write at the same absolute path in new project containers.
mounts = []
```

Each entry is a literal absolute host directory. The launcher performs no tilde, environment-variable, glob, or
relative-path expansion. It requires every source to exist, resolves symlinks to a canonical directory, rejects the
filesystem root, and mounts the directory read-write at that same absolute path inside the Sysbox container.

Configured mounts must not overlap the managed Git mounts or one another. This prevents a broad parent mount from
overriding a narrower read-only or read-write boundary through Docker mount ordering. Duplicate canonical entries are
ignored.

The parser rejects unknown keys, malformed TOML, symlink or non-regular configuration files, missing sources, and
non-directory sources. A missing `config.toml` keeps the existing no-additional-mount behavior.

This file is local machine configuration, not a portable project contract. It is ignored by Git because absolute
host paths differ between developers. It still lives in the writable worktree: project code can change it and affect
a later cold launch. Users must review local mount changes before starting a new session.

Configured mounts apply to both `codex-safe` and `agents-safe` only when a container is created. A live session keeps
its immutable mount set until it exits; editing or removing the file does not add or revoke mounts in that container.

### 3. Discovery and precedence

The only project image definition is:

```text
<canonical-worktree-root>/.agents-safe/Dockerfile
```

Rules:

- A missing `.agents-safe/` directory or Dockerfile requests no derived image.
- The `.agents-safe/` directory must be a real directory, not a symlink.
- The Dockerfile must be a regular file, not a symlink, device, socket, or directory.
- The launcher does not search parent directories, the host home, or a linked worktree's primary checkout.
- An explicitly supplied `--image` bypasses Dockerfile discovery and project-image building for that launch. It does
  not bypass local mount configuration.

Nested context traversal, `.dockerignore`, `COPY`, and layer invalidation use Docker's own build-context semantics. The
launcher does not compute a competing digest over the context.

### 4. Build and cache

The launcher first checks for a reusable deterministic session. Only the new-container path builds a project image.

The local tag is stable for the worktree and invoking user:

```text
codex-safe-project-<project-key>:local
```

For every cold create, the launcher runs `docker build` with:

- `.agents-safe/` as the complete build context;
- `.agents-safe/Dockerfile` as the Dockerfile;
- the selected base reference in `AGENTS_SAFE_BASE`;
- the stable local tag;
- ordinary Docker build network access.

Docker/BuildKit owns cache lookup and invalidation. It already understands Dockerfile instructions, `.dockerignore`,
`COPY` and `ADD` inputs, build arguments, base images, and layer dependencies. The launcher neither hashes the context
nor stores cache-identity labels.

Build progress and diagnostics use launcher stderr. Stdout remains reserved for the requested command.

The build receives no secret mounts, SSH forwarding, host environment copy, host home, Codex home, or Docker socket.
Private build credentials require a separate design.

### 5. Derived-image compatibility

After a successful build, the launcher inspects the stable tag and selects its immutable image ID. It rejects a
derived image that changes any static base-image contract:

- architecture matches the host;
- configured user is empty or root;
- entrypoint is `/usr/bin/tini -- /usr/local/bin/codex-safe-session`;
- default command is `serve`;
- `DOCKER_HOST` is `unix:///var/run/docker.sock`.

The normal session startup exercises `tini`, `codex-safe-session`, `codex`, Docker CLI, and the private daemon. Missing
or broken runtime binaries fail without a base-image fallback.

Validation does not make a Dockerfile trustworthy. The project opted into executing it by tracking the definition.

### 6. Active-session reuse

Image selection applies only when the deterministic session container is created. A compatible running session is
reused without building or inspecting a project image, even when:

- `.agents-safe/Dockerfile` changed;
- `.agents-safe/Dockerfile` was added or removed;
- `.agents-safe/config.toml` changed or was removed;
- a later invocation supplies an explicit `--image`.

This matches the existing `--image` lifecycle: a later image choice does not replace an active container. The next
cold create builds the current Dockerfile or returns to the selected base image when the Dockerfile is absent.

The launcher never terminates active commands, rebuilds a running container, or installs packages through
`docker exec`.

### 7. Failure and concurrency

The launcher fails without fallback when:

- discovery finds an invalid context directory or Dockerfile boundary;
- local mount configuration is invalid or names an unsafe source;
- the base image cannot be made available;
- Docker build fails;
- the built tag cannot be inspected;
- the derived image violates the static startup contract;
- session or relay creation fails.

Docker owns the build-context snapshot. If files change while the client sends the context, the produced image is the
result of Docker's input processing; the launcher does not attach a potentially misleading precomputed digest.

Concurrent cold callers may invoke duplicate builds against the same stable tag. The existing deterministic
container-name race still ensures that only one session container wins. Build serialization is an optimization, not
a version-one host-state contract.

### 8. Cache lifetime

The stable derived image and BuildKit layers remain in host Docker after a Sysbox session is removed. They consume
disk until ordinary Docker cleanup removes them.

Version one does not persist:

- Go module caches;
- Gradle or Maven artifact caches;
- npm package caches;
- nested-Docker images, containers, or volumes.

The launcher never discovers or mounts global host package-manager directories implicitly. A user may expose a narrow
directory explicitly through local mount configuration and accepts read-write project access to that path.

## Repository Example

This repository's tracked [`.agents-safe/Dockerfile`](../../.agents-safe/Dockerfile) copies the pinned Go 1.26.0
toolchain from the same digest-pinned image used by the session-builder stage:

```dockerfile
ARG AGENTS_SAFE_BASE

FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c \
    AS go-toolchain

FROM ${AGENTS_SAFE_BASE}

COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

RUN go version
```

It adds no system package. Adding one still requires the explicit approval mandated by
[`docs/dependencies.md`](../dependencies.md).

## Boundaries and Non-Goals

This design excludes:

- an additional project-image manifest or startup hooks;
- automatic ecosystem detection;
- arbitrary build contexts outside `.agents-safe/`;
- launcher-owned context hashing or a trust database;
- BuildKit secrets, SSH forwarding, or private-registry credential design;
- persistent package-manager or nested-Docker caches;
- automatic image and BuildKit-cache pruning;
- prebuilt project-image registry selection.

## Test Plan

Focused tests prove:

- initialization creates the exact inactive sample without constructing a Docker launcher;
- initialization is idempotent, preserves existing files, and adds exact `.gitignore` rules;
- mount configuration accepts canonical directories and rejects malformed, unknown, missing, root, file, and overlap
  cases;
- absent, valid, and invalid discovery boundaries;
- stable per-project image naming;
- explicit `--image` bypass;
- exact Docker build arguments and fixed context;
- build output routing and fail-closed errors;
- static derived-image validation;
- active-session reuse performs no build after the Dockerfile changes.

Real-host smoke proves:

- a configured external directory is visible read-write at the same absolute path in the Sysbox container;
- the public no-`--image` path builds a dependency-free v1 fixture;
- changing the Dockerfile to v2 does not mutate the active v1 session;
- the next cold create rebuilds the stable tag and executes v2;
- fixture images and containers are removed during cleanup.

## Where the code lives

- `internal/cli/`: preserves whether `--image` was explicitly supplied.
- `cmd/agents-safe/`: dispatches the host-side `init` subcommand before Docker launcher construction.
- `internal/launcher/projectenv/`: initializes local resources, parses configured mounts, discovers the fixed build
  context, and names the stable image tag.
- `internal/launcher/launchplan/`: merges validated configured mounts with the managed Git topology.
- `internal/launcher/`: runs build orchestration, validates the result, and selects its immutable ID.
- `internal/launcher/dockercli/`: provides typed Docker build and image-inspection transport.
- `tests/smoke/`: provides real Docker and Sysbox proof.

This document extends image selection in [`codex-safe.md`](codex-safe.md). Command lifetime after container creation
remains owned by [`go-session-manager.md`](go-session-manager.md).
