# BuildKit supplies TARGETARCH. The architecture-specific scratch stages below let the final
# stage copy one verified upstream binary without keeping download tools in the image.
ARG TARGETARCH

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
        tini \
    && rm -rf /var/lib/apt/lists/*

# The entrypoint reconciles the host account identity, starts the private daemon, then drops
# privileges and execs the requested probe. It creates a container-local home at the host home's
# absolute path and installs this bashrc there, enabling completion without mounting the host home.
COPY container/entrypoint.sh /usr/local/bin/codex-safe-entrypoint
COPY container/bashrc /etc/codex-safe/bashrc
COPY --from=crun --chmod=0755 /crun /usr/local/bin/crun

# Fail the image build immediately if the downloaded binary is unusable on the target platform.
RUN chmod 0755 /usr/local/bin/codex-safe-entrypoint \
    && /usr/local/bin/crun --version

# Both the probe and nested containers use only the daemon socket created inside the outer
# container. This path is not a bind mount of /var/run/docker.sock from the host.
ENV DOCKER_HOST=unix:///var/run/docker.sock

# Ubuntu's built-in C.UTF-8 locale lets Bash, readline, Git, and text tools treat Cyrillic input
# and output as UTF-8 without generating or installing a language-specific locale.
ENV LANG=C.UTF-8

# Tini is PID 1 so orphaned nested-daemon children are reaped and signals reach the entrypoint.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/codex-safe-entrypoint"]
