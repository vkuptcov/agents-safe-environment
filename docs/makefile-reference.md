# Makefile Reference

Run these commands from the repository root.

| Target | Purpose |
| --- | --- |
| `make build` | Build host and container-side Go binaries under `bin/`. |
| `make install` | Install the host launchers into Go's binary directory and build the local outer image. |
| `make docker-build` | Build the local `codex-safe-mvp:local` outer image. |
| `make install-tools` | Compile all tools declared by `tools/go.mod` into the ignored local `bin/` directory. |
| `make lint` | Run the pinned GolangCI-Lint tool against the application module. |
| `make lint-n-fix` | Apply supported GolangCI-Lint and formatter fixes, then report remaining issues. |
| `make test` | Run Go tests, vet both modules, and syntax-check all container shell files. |
| `make test-smoke-go` | Run the real Sysbox and linked-worktree smoke flow. |
| `make check-docs` | Validate Mermaid blocks, local documentation links, and architecture module paths. |
| `make check-doc-links` | Build and run the containerized link and architecture-path validator. |
| `make check-mermaid` | Build the pinned validator image and parse Mermaid blocks under `docs/` and in `ARCHITECTURE.md`. |

`make install-tools` uses Go's `tool` meta-pattern, so adding another `tool` directive makes it part of the same
installation target. The lint targets depend on `bin/golangci-lint` and install or rebuild it when the binary is
missing or the tools module changes. This does not modify the application dependency graph, but the first install
may download Go modules. `make lint-n-fix` rewrites files in place, so review its diff and run `make lint` and the
applicable test gate afterward. The compiled linter still invokes the project Go toolchain to load and analyze Go
packages, so the repository's required Go version must remain available when either lint target runs.

`make install` uses the standard Go installation destination: `GOBIN` when it is set, otherwise the `bin`
subdirectory of the first `GOPATH` entry. It installs only the host-side `codex-safe` and `agents-safe` launchers;
the container-side `codex-safe-session` binary remains part of repository-local and image builds.

Both documentation validators require Docker. Their first builds may require registry and package-index access.
