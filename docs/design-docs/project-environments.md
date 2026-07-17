# Project-Specific Agent Environments

Status: Proposed

Decision: the owner accepted the first implementation scope on 2026-07-17.

Scope:

- project-owned system toolchains and packages needed inside `codex-safe` and `agents-safe` sessions;
- automatic discovery and host-side build of `.agents-safe/Dockerfile`;
- project-image caching, compatibility checks, confirmation, and active-session reuse;
- behavior of the existing explicit `--image` override when a project Dockerfile exists.

## Purpose and Intent

### Problem

Different projects need different tools. One needs Go, another needs a JDK and Gradle, and another needs Node.js,
`protoc`, or system libraries. Installing every toolchain in the shared base image makes that image large and cannot
satisfy projects that require different versions.

Installing packages manually inside a running session is also a poor default. The Sysbox container is removed after
the final managed command and idle timeout, so its writable layer does not survive into the next session.

The launcher needs a project-owned, reproducible way to add tools without mounting host SDKs or adding a package
manager for every ecosystem.

### Worked Example

Before:

```text
The user opens a Java project with codex-safe.
The image has no JDK.
The user installs a JDK with sudo.
The installation disappears when the session ends.
```

After:

```text
The project contains .agents-safe/Dockerfile.
The user runs codex-safe as usual.
The launcher confirms and builds a project image once.
Later sessions reuse the host Docker image and layer cache.
```

The project contract is one file:

```dockerfile
ARG AGENTS_SAFE_BASE
FROM ${AGENTS_SAFE_BASE}

RUN apt-get update \
    && apt-get install --yes --no-install-recommends openjdk-21-jdk-headless \
    && rm -rf /var/lib/apt/lists/*
```

The package command is illustrative. Real projects must pin versions, sources, images, and checksums according to
their dependency policy.

### Chosen Shape

Both launchers look for `.agents-safe/Dockerfile` at the canonical worktree root. When it is absent, the existing
base-image behavior is unchanged. When it is present, the launcher builds a derived image through the host Docker
daemon and starts the normal Sysbox session from that image.

The build context is exactly `.agents-safe/`. The launcher passes the selected base image through
`AGENTS_SAFE_BASE`; the project Dockerfile owns every additional tool and version.

The project-definition digest identifies the requested environment. The base image ID joins that digest to identify
the cached derived image. A changed project definition or base image therefore selects a new cached image.

### Success Criteria

- A project adds one Dockerfile and users continue to run ordinary `codex-safe` or `agents-safe` commands.
- The first launch confirms and builds the project image; later launches reuse a valid cached image without a prompt.
- Two projects can use different toolchain versions without sharing writable SDK state.
- The build receives no host home, Codex home, credentials, project files outside `.agents-safe/`, or Docker socket.
- A derived image preserves the session manager, Codex CLI, nested Docker, and root bootstrap contracts.
- A changed Dockerfile never mutates an active session or installs packages through `docker exec`.
- A build or compatibility failure stops the launch without falling back to the base image.

### Tradeoff

The first launch performs a host-side Docker build and may download large layers. Project images and BuildKit cache
remain in host Docker after a Sysbox session is removed and consume disk until the user cleans them up.

This cost buys reproducible, cross-session toolchains without exposing host SDK directories to the agent.

## Contract

### 1. Discovery and precedence

The only project definition in version 1 is:

```text
<canonical-worktree-root>/.agents-safe/Dockerfile
```

Rules:

- A missing `.agents-safe/` directory or Dockerfile means the project requests no derived image.
- The directory, Dockerfile, and every build-context entry must remain inside the canonical worktree.
- The launcher rejects symlinks, devices, sockets, and other non-regular context entries.
- The launcher does not search parent directories, the host home, or a linked worktree's primary checkout.
- An explicitly supplied `--image` bypasses project-environment discovery for that launch.

`--image` retains its current lifecycle meaning: it selects an image only when creating a session and does not replace
an already-running compatible session.

### 2. Definition digest and cached image

The project-definition digest is SHA-256 over sorted relative paths, entry types, executable bits, and file contents
under `.agents-safe/`. Absolute checkout paths, UID, GID, and timestamps do not affect it.

The project-image cache key is SHA-256 over:

- the project-definition digest;
- the resolved immutable base image ID;
- the project-image contract version.

The local image name contains the existing project key and a shortened cache key. Full values remain in image labels:

- `codex-safe.project-image=true`;
- `codex-safe.project-key=<project-key>`;
- `codex-safe.project-definition=<full-definition-digest>`;
- `codex-safe.base-image-id=<immutable-base-image-id>`.

Only an image whose labels and compatibility checks match the current request counts as cached. A missing or invalid
cached image follows the confirmation and build path.

### 3. Confirmation and build

Before building a new cache key, an interactive launcher prints the Dockerfile and context paths and asks:

```text
Project defines a custom environment:
  /work/app/.agents-safe/Dockerfile
Build it with the host Docker daemon? [y/N]
```

Only `y` or `yes` accepts. A decline, EOF, or non-interactive launch fails before `docker build`. There is no durable
trust database in version 1: the presence of a valid cached image records that the exact cache key was previously
built successfully. If the image is removed, confirmation is required again.

The launcher invokes Docker with separate arguments and sends build progress to its diagnostic stream. Standard
output remains reserved for the requested command.

The build receives:

- `.agents-safe/` as its only context;
- the selected base reference as `AGENTS_SAFE_BASE`;
- launcher-owned labels and a deterministic local tag;
- ordinary Docker build network access.

The build receives no secret mounts, SSH forwarding, host environment copy, or implicit credentials. Projects that
need private build credentials require a separate design.

### 4. Derived-image compatibility

A project image must preserve these base-image contracts:

- root is the configured image user for container bootstrap;
- entrypoint is `/usr/bin/tini -- /usr/local/bin/codex-safe-session`;
- default command is `serve`;
- `DOCKER_HOST` selects the private daemon at `unix:///var/run/docker.sock`;
- `tini`, `codex-safe-session`, `codex`, and the Docker CLI remain executable.

The launcher inspects the image configuration and runs bounded, mount-free probes before creating a Sysbox session.
The probes use the immutable derived image ID, no host network, no capabilities, and no writable host paths.

These checks detect accidental incompatibility. They do not make a project Dockerfile safe or prove that its added
software is trustworthy; the user explicitly approved executing that Dockerfile through host Docker.

### 5. Active-session reuse

New session containers carry `codex-safe.project-environment` with one of these values:

- `absent`: no project Dockerfile selected the image;
- `override`: the user explicitly supplied `--image`;
- `sha256:<definition-digest>`: a project Dockerfile selected the image.

Reuse follows these rules:

- An explicit `--image` keeps current behavior and may reuse the already-running managed session.
- A requested project definition requires the running label to contain the same definition digest.
- An absent project definition accepts a missing legacy label, `absent`, or `override`.
- Removing a Dockerfile does not silently reuse a running project-image session.
- A mismatch reports both environments and asks the user to finish the active session.

The launcher never terminates active commands, rebuilds a running container, or adds packages through `docker exec`.
After the old session exits, the next launch resolves or builds the requested image normally.

The base image ID is not compared against an active session. This preserves the existing rule that image selection
applies at container creation; a newer base takes effect after the current session ends.

### 6. Failure and change behavior

The launcher fails without fallback when:

- context validation or hashing fails;
- the user declines or cannot answer the build prompt;
- the base image cannot be pulled or resolved;
- Docker build fails;
- `.agents-safe/` changes while its image is being built;
- the base reference moves to another image ID during the build;
- the derived image fails label or compatibility validation;
- an active session has a different project definition.

A concurrent first launch may perform duplicate builds in version 1. Existing deterministic container creation still
ensures that only one session wins and later callers inspect and reuse that session. Avoiding redundant concurrent
build work is an optimization, not a new correctness or host-state contract for this version.

### 7. What is and is not cached

The derived image persists system toolchains and packages in host Docker layers across Sysbox sessions.

Version 1 does not persist:

- Go module caches;
- Gradle or Maven artifact caches;
- npm package caches;
- nested-Docker images, containers, or volumes.

Persistent package-manager caches require a separate project-scoped volume, ownership, and cleanup design. The
launcher must not mount the host's global `~/.gradle`, `~/.m2`, Go, npm, or SDK directories as a shortcut.

### 8. Example for this repository

Both this repository's application and tools modules require Go 1.26.0. The runtime image already contains Docker,
Compose, Git, Make, Bash, and `rg`, but its Go compiler exists only in a build stage.

An eventual project Dockerfile can reuse the exact pinned Go image:

```dockerfile
ARG AGENTS_SAFE_BASE

FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c \
    AS go-toolchain

FROM ${AGENTS_SAFE_BASE}

COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

RUN apt-get update \
    && apt-get install --yes --no-install-recommends build-essential \
    && rm -rf /var/lib/apt/lists/* \
    && go version
```

That environment supports `make build`, `make test`, `make lint`, `make docker-build`, and `make check-docs` inside
the session. `make test-smoke-go` remains a real-host gate because it needs a host Docker Engine with
`sysbox-runc` registered.

This example is not itself approval to add `build-essential`. Checking the Dockerfile into this repository requires
the explicit dependency approval mandated by [`docs/dependencies.md`](../dependencies.md).

## Boundaries and Non-Goals

This design intentionally excludes:

- `environment.toml` or another project-environment manifest;
- automatic detection of `go.mod`, Gradle, Maven, npm, `mise`, or other ecosystem files;
- a launcher-owned catalog or installer for Go, Java, Node.js, or other toolchains;
- startup or post-create hooks;
- prebuilt registry image selection;
- arbitrary build contexts outside `.agents-safe/`;
- BuildKit secrets, SSH forwarding, or private-registry credential design;
- persistent package-manager or nested-Docker caches;
- automatic pruning of project images and build cache;
- checking this repository's example `.agents-safe/Dockerfile` into source without dependency approval.

Rejected alternative: install packages at session startup. It repeats downloads after every session, makes ordinary
launch depend on registries, and produces a mutable environment.

Rejected alternative: mount host SDKs or package-manager homes. Host binaries may not match the container ABI, and
the mounts expose unrelated writable host state.

Rejected alternative: put every common SDK in the base image. It grows the common image and still cannot support
projects that require conflicting versions.

Rejected alternative: require nested project containers for every command. They remain useful for services and
tests, but they do not provide toolchains to the agent session itself.

## Test Plan

### Focused tests

- Discover an absent, valid, changed, and invalid `.agents-safe/` context.
- Prove digest stability across checkout paths and timestamps, and changes on content or executable-bit updates.
- Prove explicit `--image` bypasses project discovery and retains current active-session semantics.
- Prove a new cache key prompts, a valid cached image does not prompt, and decline or non-interactive input fails.
- Assert exact Docker build arguments, fixed context, labels, build argument, and diagnostic-stream routing.
- Reject a context or base image that changes during build.
- Reject derived images with incompatible user, entrypoint, command, environment, labels, or required binaries.
- Prove build and probe failures never reach container creation or fallback.
- Prove every active-session reuse combination described above.

### Real-host smoke tests

- Build a dependency-free fixture image that adds one executable through `.agents-safe/Dockerfile`.
- Run that executable through `agents-safe` in a real Sysbox session.
- Reuse the cached image and active session without another prompt.
- Change the fixture definition, reject active-session reuse, then use the new image after the old session ends.
- Preserve nested Docker, linked-worktree mounts, UID/GID ownership, Codex mounts, and host-socket isolation.
- Remove all fixture images and containers during test cleanup.

## Where the code lives

- `internal/cli/`: preserve whether `--image` was explicitly supplied.
- `internal/launcher/projectenv/`: discover, validate, hash, and name project definitions and cached images.
- `internal/launcher/`: confirmation, build orchestration, image selection, and session compatibility policy.
- `internal/launcher/dockercli/`: typed Docker build, image inspection, and compatibility probe transport.
- `internal/launcher/docker_requests.go`: project-environment session label.
- `tests/smoke/`: real Docker and Sysbox proof.
- `container/Dockerfile`: authoritative base entrypoint, command, environment, and required binary contract.

This document extends the image-selection and reuse behavior in
[`Safe Environment for Running Codex Agents`](codex-safe.md). Command lifetime after container creation remains owned
by [`Go Session Manager`](go-session-manager.md).
