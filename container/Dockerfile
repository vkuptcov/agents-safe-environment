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

RUN chmod 0755 /usr/local/bin/codex-safe-entrypoint

ENV DOCKER_HOST=unix:///var/run/docker.sock

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/codex-safe-entrypoint"]
