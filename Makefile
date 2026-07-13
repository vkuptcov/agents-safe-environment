GO := go
BINARY := bin/codex-safe

.PHONY: build test

build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/codex-safe

test:
	$(GO) test ./...
	$(GO) vet ./...
	bash -n container/bashrc container/entrypoint.sh tests/smoke/sysbox-linked-worktree.sh
