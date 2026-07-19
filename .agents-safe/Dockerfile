# This definition is intentionally self-contained. The default keeps direct Docker builds valid;
# the launcher replaces it with the selected pinned runtime image.
ARG AGENTS_SAFE_BASE=codex-safe-mvp:local

FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c \
    AS go-toolchain

FROM ${AGENTS_SAFE_BASE}

COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

RUN go version
