GO := go
DOCKER := docker
BINARY := bin/codex-safe
SESSION_BINARY := bin/codex-safe-session
IMAGE := codex-safe-mvp:local
SMOKE_DIR := tests/smoke

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
	$(GO) -C $(SMOKE_DIR) test ./...
	$(GO) -C $(SMOKE_DIR) vet ./...
	bash -n container/bashrc tests/smoke/sysbox-linked-worktree.sh

test-smoke-go: build docker-build
	CODEX_SAFE_RUN_SYSBOX_SMOKE=1 $(GO) -C $(SMOKE_DIR) test . -run TestSysboxLinkedWorktreeGo -count=1 -v
