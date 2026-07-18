# Project-Specific Agent Environments

Status: Implemented

Decision: the owner accepted Dockerfile-as-consent and BuildKit-owned cache semantics on 2026-07-18.

Scope:

- project-owned system toolchains and packages needed inside `codex-safe` and `agents-safe` sessions;
- automatic host-side builds from `.agents-safe/Dockerfile`;
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

### 1. Discovery and precedence

The only project definition is:

```text
<canonical-worktree-root>/.agents-safe/Dockerfile
```

Rules:

- A missing `.agents-safe/` directory or Dockerfile requests no derived image.
- The `.agents-safe/` directory must be a real directory, not a symlink.
- The Dockerfile must be a regular file, not a symlink, device, socket, or directory.
- The launcher does not search parent directories, the host home, or a linked worktree's primary checkout.
- An explicitly supplied `--image` bypasses project-environment discovery and building for that launch.

Nested context traversal, `.dockerignore`, `COPY`, and layer invalidation use Docker's own build-context semantics. The
launcher does not compute a competing digest over the context.

### 2. Build and cache

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

### 3. Derived-image compatibility

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

### 4. Active-session reuse

Image selection applies only when the deterministic session container is created. A compatible running session is
reused without building or inspecting a project image, even when:

- `.agents-safe/Dockerfile` changed;
- `.agents-safe/Dockerfile` was added or removed;
- a later invocation supplies an explicit `--image`.

This matches the existing `--image` lifecycle: a later image choice does not replace an active container. The next
cold create builds the current Dockerfile or returns to the selected base image when the Dockerfile is absent.

The launcher never terminates active commands, rebuilds a running container, or installs packages through
`docker exec`.

### 5. Failure and concurrency

The launcher fails without fallback when:

- discovery finds an invalid context directory or Dockerfile boundary;
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

### 6. Cache lifetime

The stable derived image and BuildKit layers remain in host Docker after a Sysbox session is removed. They consume
disk until ordinary Docker cleanup removes them.

Version one does not persist:

- Go module caches;
- Gradle or Maven artifact caches;
- npm package caches;
- nested-Docker images, containers, or volumes.

The launcher must not mount global host package-manager directories as a shortcut.

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

- a project-environment manifest or startup hooks;
- automatic ecosystem detection;
- arbitrary build contexts outside `.agents-safe/`;
- launcher-owned context hashing or a trust database;
- BuildKit secrets, SSH forwarding, or private-registry credential design;
- persistent package-manager or nested-Docker caches;
- automatic image and BuildKit-cache pruning;
- prebuilt project-image registry selection.

## Test Plan

Focused tests prove:

- absent, valid, and invalid discovery boundaries;
- stable per-project image naming;
- explicit `--image` bypass;
- exact Docker build arguments and fixed context;
- build output routing and fail-closed errors;
- static derived-image validation;
- active-session reuse performs no build after the Dockerfile changes.

Real-host smoke proves:

- the public no-`--image` path builds a dependency-free v1 fixture;
- changing the Dockerfile to v2 does not mutate the active v1 session;
- the next cold create rebuilds the stable tag and executes v2;
- fixture images and containers are removed during cleanup.

## Where the code lives

- `internal/cli/`: preserves whether `--image` was explicitly supplied.
- `internal/launcher/projectenv/`: discovers the fixed context and names the stable image tag.
- `internal/launcher/`: runs build orchestration, validates the result, and selects its immutable ID.
- `internal/launcher/dockercli/`: provides typed Docker build and image-inspection transport.
- `tests/smoke/`: provides real Docker and Sysbox proof.

This document extends image selection in [`codex-safe.md`](codex-safe.md). Command lifetime after container creation
remains owned by [`go-session-manager.md`](go-session-manager.md).
