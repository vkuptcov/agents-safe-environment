GO := go
DOCKER := docker
CODEX_BINARY := bin/codex-safe
AGENTS_BINARY := bin/agents-safe
SESSION_BINARY := bin/codex-safe-session
IMAGE := codex-safe-mvp:local
SMOKE_DIR := tests/smoke
GOLANGCI_LINT_MODFILE := tools/go.mod
TOOLS_BIN_DIR := bin
GOLANGCI_LINT_BINARY := $(TOOLS_BIN_DIR)/golangci-lint

.PHONY: build install docker-build install-tools lint lint-n-fix test test-smoke-go check-docs check-doc-links \
	check-mermaid

build:
	mkdir -p $(dir $(CODEX_BINARY))
	$(GO) build -o $(CODEX_BINARY) ./cmd/codex-safe
	$(GO) build -o $(AGENTS_BINARY) ./cmd/agents-safe
	$(GO) build -o $(SESSION_BINARY) ./cmd/codex-safe-session

install: docker-build
	$(GO) install ./cmd/codex-safe ./cmd/agents-safe

docker-build:
	$(DOCKER) build -t $(IMAGE) -f container/Dockerfile .

install-tools: $(GOLANGCI_LINT_BINARY)

$(GOLANGCI_LINT_BINARY): $(GOLANGCI_LINT_MODFILE) tools/go.sum
	mkdir -p $(TOOLS_BIN_DIR)
	GOBIN=$(CURDIR)/$(TOOLS_BIN_DIR) $(GO) install -modfile=$(GOLANGCI_LINT_MODFILE) tool

lint: $(GOLANGCI_LINT_BINARY)
	$(GOLANGCI_LINT_BINARY) run ./...

lint-n-fix: $(GOLANGCI_LINT_BINARY)
	$(GOLANGCI_LINT_BINARY) run --fix ./...

test:
	$(GO) test ./...
	$(GO) vet ./...
	$(GO) -C $(SMOKE_DIR) test ./...
	$(GO) -C $(SMOKE_DIR) vet ./...
	bash -n container/bashrc
	sh -n container/codex-dispatcher container/codex-safe-update

test-smoke-go: build docker-build
	CODEX_SAFE_RUN_SYSBOX_SMOKE=1 $(GO) -C $(SMOKE_DIR) test . -run TestSysbox -count=1 -v

check-docs: check-mermaid check-doc-links

check-doc-links:
	@command -v $(DOCKER) >/dev/null 2>&1 || { echo "check-doc-links: docker is required"; exit 1; }
	@$(DOCKER) build -q -t doc-links-check harness/doc-links-check >/dev/null
	@$(DOCKER) run --rm -v "$(CURDIR):/work:ro" doc-links-check

check-mermaid:
	@command -v $(DOCKER) >/dev/null 2>&1 || { echo "check-mermaid: docker is required"; exit 1; }
	@$(DOCKER) build -q -t mermaid-check harness/mermaid-check >/dev/null
	@$(DOCKER) run --rm -v "$(CURDIR):/work:ro" mermaid-check docs ARCHITECTURE.md
