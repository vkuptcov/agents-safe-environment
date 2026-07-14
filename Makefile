GO := go
DOCKER := docker
BINARY := bin/codex-safe
SESSION_BINARY := bin/codex-safe-session
IMAGE := codex-safe-mvp:local

.PHONY: build docker-build test

build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/codex-safe
	$(GO) build -o $(SESSION_BINARY) ./cmd/codex-safe-session

docker-build:
	$(DOCKER) build -t $(IMAGE) -f container/Dockerfile .

test:
	$(GO) test ./...
	$(GO) vet ./...
	bash -n container/bashrc container/entrypoint.sh tests/smoke/sysbox-linked-worktree.sh
