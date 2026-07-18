# This definition is intentionally self-contained: the launcher supplies the pinned runtime image
# through AGENTS_SAFE_BASE, and the only additional toolchain is the Go version declared in go.mod.
ARG AGENTS_SAFE_BASE

FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c \
    AS go-toolchain

FROM ${AGENTS_SAFE_BASE}

COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

RUN go version
