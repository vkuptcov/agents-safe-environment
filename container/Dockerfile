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
COPY cmd/codex-safe-session/ cmd/codex-safe-session/
COPY internal/container/ internal/container/
COPY internal/session/ internal/session/
COPY internal/terminal/ internal/terminal/
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags='-s -w' -o /out/codex-safe-session ./cmd/codex-safe-session

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

# Codex is the product's single executable. It ships as a standalone musl release pinned to
# rust-v0.144.4 and verified per-arch by SHA-256, chosen over the npm package to keep Node out of
# the image and pin one checksummed artifact. Each scratch stage holds the verified archive; the
# final stage extracts the one entry into an image-owned path that a mounted Codex home cannot
# shadow through PATH. Updating Codex requires a new image build with no startup network install.
FROM scratch AS codex-amd64
ADD --checksum=sha256:37c985be9d89e8c4f43b3aa0594c1213eac212d30ae2b95221f08fec807515d1 \
    https://github.com/openai/codex/releases/download/rust-v0.144.4/codex-x86_64-unknown-linux-musl.tar.gz \
    /codex.tar.gz

FROM scratch AS codex-arm64
ADD --checksum=sha256:4d07243ef4ae6786b8b321d7aea3f9be4e1d2c597ae5407e7c1b9873334082b2 \
    https://github.com/openai/codex/releases/download/rust-v0.144.4/codex-aarch64-unknown-linux-musl.tar.gz \
    /codex.tar.gz

FROM codex-${TARGETARCH} AS codex

# Pin the base by digest so a rebuild does not silently consume a different Ubuntu image.
FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90

ARG DEBIAN_FRONTEND=noninteractive

# docker.io intentionally supplies both the client and a daemon: the daemon runs inside the
# Sysbox container, while the host Docker socket is never mounted. The remaining packages are
# the minimum interactive tooling exercised by the MVP smoke test.
RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        bash \
        bash-completion \
        ca-certificates \
        docker-compose-v2 \
        docker.io \
        git \
        less \
        make \
        ripgrep \
        sudo \
        tini \
    && rm -rf /var/lib/apt/lists/*

# The Go entrypoint reconciles the host account, prepares its home and sudo policy, starts the
# private daemon, and owns the session manager state machine. Managed commands enter later through
# `docker exec` as the recreated user; the root entrypoint never receives their argv.
COPY container/bashrc /etc/codex-safe/bashrc
COPY --from=crun --chmod=0755 /crun /usr/local/bin/crun
COPY --from=session-builder --chmod=0755 /out/codex-safe-session /usr/local/bin/codex-safe-session

# Extract the single archive entry codex-<target> into the image-owned Codex path. The archive was
# already checksum-verified during ADD; installing here keeps download tools out of the final image.
COPY --from=codex /codex.tar.gz /tmp/codex.tar.gz
RUN tar -xzf /tmp/codex.tar.gz -C /tmp \
    && install -m 0755 /tmp/codex-*-unknown-linux-musl /usr/local/bin/codex \
    && rm -rf /tmp/codex.tar.gz /tmp/codex-*-unknown-linux-musl

# Fail the image build immediately if any copied binary is unusable on the target platform, and
# pin the Codex version so an unexpected artifact cannot pass silently.
RUN /usr/local/bin/crun --version \
    && /usr/local/bin/codex-safe-session --help \
    && /usr/local/bin/codex --version | grep -q 'codex-cli 0.144.4'

# Both Codex and nested containers use only the daemon socket created inside the outer
# container. This path is not a bind mount of /var/run/docker.sock from the host.
ENV DOCKER_HOST=unix:///var/run/docker.sock

# Ubuntu's built-in C.UTF-8 locale lets Bash, readline, Git, and text tools treat Cyrillic input
# and output as UTF-8 without generating or installing a language-specific locale.
ENV LANG=C.UTF-8

# xterm-256color has terminfo support in the image and tells interactive tools that the attached
# host terminal accepts ANSI colors. Tools using automatic color still disable it for non-TTY output.
ENV TERM=xterm-256color

# Tini remains PID 1 only to reap orphaned descendants and forward signals. The Go session manager
# is the container's single lifecycle-owning workload and gracefully stops its dockerd child.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/codex-safe-session", "serve"]
