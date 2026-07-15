GO := go
DOCKER := docker
CODEX_BINARY := bin/codex-safe
AGENTS_BINARY := bin/agents-safe
SESSION_BINARY := bin/codex-safe-session
PROBE_BINARY := bin/codex-safe-probe
IMAGE := codex-safe-mvp:local
SMOKE_DIR := tests/smoke

.PHONY: build build-smoke-probe docker-build test test-smoke-go check-docs check-doc-links check-mermaid

build:
	mkdir -p $(dir $(CODEX_BINARY))
	$(GO) build -o $(CODEX_BINARY) ./cmd/codex-safe
	$(GO) build -o $(AGENTS_BINARY) ./cmd/agents-safe
	$(GO) build -o $(SESSION_BINARY) ./cmd/codex-safe-session

# The smoke transport is compiled only under the smoke tag, so the product binary above never
# carries an arbitrary-command execution surface. Real-host smoke tests drive this probe instead.
build-smoke-probe:
	mkdir -p $(dir $(PROBE_BINARY))
	$(GO) build -tags smoke -o $(PROBE_BINARY) ./cmd/codex-safe-probe

docker-build:
	$(DOCKER) build -t $(IMAGE) -f container/Dockerfile .

test:
	$(GO) test ./...
	$(GO) vet ./...
	$(GO) -C $(SMOKE_DIR) test ./...
	$(GO) -C $(SMOKE_DIR) vet ./...
	bash -n container/bashrc

test-smoke-go: build build-smoke-probe docker-build
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
