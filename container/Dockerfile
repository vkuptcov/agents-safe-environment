# BuildKit supplies TARGETARCH. The architecture-specific scratch stages below let the final
# stage copy one verified upstream binary without keeping download tools in the image.
ARG TARGETARCH
ARG TARGETOS=linux

# Build the container-side manager in a stage pinned to the Go version declared by go.mod. The
# static binary contains both the privileged `serve` entrypoint and unprivileged `run` wrapper;
# the final interactive image does not retain the compiler, source tree, or module cache.
FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c \
    AS session-builder
ARG TARGETARCH
ARG TARGETOS
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY cmd/agents-safe-session/ cmd/agents-safe-session/
COPY internal/container/ internal/container/
COPY internal/mcpchannel/ internal/mcpchannel/
COPY internal/relay/ internal/relay/
COPY internal/session/ internal/session/
COPY internal/terminal/ internal/terminal/
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags='-s -w' -o /out/agents-safe-session ./cmd/agents-safe-session

# crun is the low-level OCI runtime used by the Docker daemon inside the Sysbox container. A
# project directory reaches a nested container through two bind mounts: host Docker first mounts
# it into the outer Sysbox container, then inner Docker mounts that already-mounted path again.
# With a target path containing spaces, Ubuntu's runc 1.3.4 completed the initial bind but failed
# its follow-up remount while reapplying the inherited MS_NOATIME flag; the kernel returned EINVAL.
# crun 1.28 handles the same nested bind successfully. Checksums make the remote ADD reproducible
# and fail the build if the pinned release asset changes.
FROM scratch AS crun-amd64
ADD --checksum=sha256:2aa6b7024a9c9f153895c0d11ae233d3758f54844011c3a039e3e89048d01d42 \
    https://github.com/containers/crun/releases/download/1.28/crun-1.28-linux-amd64 \
    /crun

FROM scratch AS crun-arm64
ADD --checksum=sha256:cc1e8ec89aef1422e0741be196f9ed099e2e09d2f48f30f27cd44a22ef1f0342 \
    https://github.com/containers/crun/releases/download/1.28/crun-1.28-linux-arm64 \
    /crun

FROM crun-${TARGETARCH} AS crun

# Pin the base by digest so a rebuild does not silently consume a different Ubuntu image.
FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90

ARG DEBIAN_FRONTEND=noninteractive

# docker.io intentionally supplies both the client and a daemon: the daemon runs inside the
# Sysbox container, while the host Docker socket is never mounted. nodejs is the runtime the
# Claude Code plugin hooks (`.mjs` session and stop-review scripts) shell out to; without it the
# Stop hook fails with `node: not found`. The remaining packages are the minimum interactive
# tooling exercised by the MVP smoke test.
RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        bash \
        bash-completion \
        ca-certificates \
        curl \
        docker-buildx \
        docker-compose-v2 \
        docker.io \
        git \
        less \
        make \
        nodejs \
        ripgrep \
        sudo \
        tini \
    && docker buildx version \
    && node --version \
    && rm -rf /var/lib/apt/lists/*

# The Go entrypoint reconciles the host account, prepares its home and sudo policy, starts the
# private daemon, and owns the session manager state machine. Managed commands enter later through
# `docker exec` as the recreated user; the root entrypoint never receives their argv.
COPY container/bashrc /etc/agents-safe/bashrc
COPY --from=crun --chmod=0755 /crun /usr/local/bin/crun
COPY --from=session-builder --chmod=0755 /out/agents-safe-session /usr/local/bin/agents-safe-session
COPY --chmod=0755 container/codex-safe-update /usr/local/bin/codex-safe-update
COPY --chmod=0755 container/claude-safe-update /usr/local/bin/claude-safe-update

# Fail the image build immediately if any copied binary is unusable on the target platform, and
# verify the updater wrapper before the post-build volume installation runs.
RUN /usr/local/bin/crun --version \
    && /usr/local/bin/agents-safe-session --help \
    && test -x /usr/local/bin/codex-safe-update \
    && test -x /usr/local/bin/claude-safe-update \
    && sh -n /usr/local/bin/codex-safe-update \
    && bash -n /usr/local/bin/claude-safe-update

# Both agent products and nested containers use only the daemon socket created inside the outer
# container. This path is not a bind mount of /var/run/docker.sock from the host.
ENV DOCKER_HOST=unix:///var/run/docker.sock

# Product launchers invoke their volume binaries by absolute path. This PATH also makes both
# installations available in interactive `agents-safe` shells.
ENV PATH="/opt/agents-safe/codex/bin:/opt/agents-safe/claude/home/.local/bin:${PATH}"

# Claude Code's installation volume is read-only in project sessions. Updates are an explicit
# host-side maintenance operation, so the in-session background updater must not attempt to write it.
ENV DISABLE_AUTOUPDATER=1

# Project environments and repository validation build images through the private Docker daemon.
# Select BuildKit so Docker receives the architecture arguments this Dockerfile's multi-architecture
# stages require; docker-buildx supplies the Docker CLI backend in the interactive runtime image.
ENV DOCKER_BUILDKIT=1

# Ubuntu's built-in C.UTF-8 locale lets Bash, readline, Git, and text tools treat Cyrillic input
# and output as UTF-8 without generating or installing a language-specific locale.
ENV LANG=C.UTF-8

# xterm-256color has terminfo support in the image and tells interactive tools that the attached
# host terminal accepts ANSI colors. Tools using automatic color still disable it for non-TTY output.
ENV TERM=xterm-256color

# Tini remains PID 1 only to reap orphaned descendants and forward signals. The Go session manager
# is the container's single lifecycle-owning workload and gracefully stops its dockerd child.
#
# The entrypoint owns supervision and binary selection; the container's role stays a Docker command.
# The session container supplies none, so Docker appends the default below and its effective process
# is unchanged. The relay sidecar supplies a command beginning with `relay`, which replaces the
# default without bypassing tini and without a second image.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/agents-safe-session"]
CMD ["serve"]
