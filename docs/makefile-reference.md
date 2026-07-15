# Makefile Reference

Run these commands from the repository root.

| Target | Purpose |
| --- | --- |
| `make build` | Build host and container-side Go binaries under `bin/`. |
| `make docker-build` | Build the local `codex-safe-mvp:local` outer image. |
| `make test` | Run Go tests, vet both modules, and validate `container/bashrc`. |
| `make test-smoke-go` | Run the real Sysbox and linked-worktree smoke flow. |
| `make check-docs` | Validate Mermaid blocks, local documentation links, and architecture module paths. |
| `make check-doc-links` | Build and run the containerized link and architecture-path validator. |
| `make check-mermaid` | Build the pinned validator image and parse Mermaid blocks under `docs/` and in `ARCHITECTURE.md`. |

Both documentation validators require Docker. Their first builds may require registry and package-index access.
