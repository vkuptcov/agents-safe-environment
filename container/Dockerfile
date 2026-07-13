ARG TARGETARCH

FROM scratch AS crun-amd64
ADD --checksum=sha256:2aa6b7024a9c9f153895c0d11ae233d3758f54844011c3a039e3e89048d01d42 \
    https://github.com/containers/crun/releases/download/1.28/crun-1.28-linux-amd64 \
    /crun

FROM scratch AS crun-arm64
ADD --checksum=sha256:cc1e8ec89aef1422e0741be196f9ed099e2e09d2f48f30f27cd44a22ef1f0342 \
    https://github.com/containers/crun/releases/download/1.28/crun-1.28-linux-arm64 \
    /crun

FROM crun-${TARGETARCH} AS crun

FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        bash \
        ca-certificates \
        docker.io \
        git \
        tini \
    && rm -rf /var/lib/apt/lists/*

COPY container/entrypoint.sh /usr/local/bin/codex-safe-entrypoint
COPY --from=crun --chmod=0755 /crun /usr/local/bin/crun

RUN chmod 0755 /usr/local/bin/codex-safe-entrypoint \
    && /usr/local/bin/crun --version

ENV DOCKER_HOST=unix:///var/run/docker.sock

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/codex-safe-entrypoint"]
