GO := go
DOCKER := docker
BINARY := bin/codex-safe
SESSION_BINARY := bin/codex-safe-session
IMAGE := codex-safe-mvp:local

.PHONY: build docker-build test test-smoke-go

build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/codex-safe
	$(GO) build -o $(SESSION_BINARY) ./cmd/codex-safe-session

docker-build:
	$(DOCKER) build -t $(IMAGE) -f container/Dockerfile .

test:
	$(GO) test ./...
	$(GO) vet ./...
	bash -n container/bashrc tests/smoke/sysbox-linked-worktree.sh

test-smoke-go: build docker-build
	CODEX_SAFE_RUN_SYSBOX_SMOKE=1 $(GO) test ./tests/smoke -run TestSysboxLinkedWorktreeGo -count=1 -v
