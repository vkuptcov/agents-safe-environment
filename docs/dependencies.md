# Dependencies

Ask for explicit owner approval before adding a runtime, build, test, container, or documentation dependency.

## Go Dependencies

- Keep `go.mod` and `go.sum` synchronized.
- Prefer the standard library when it keeps the implementation clear and maintainable.
- Explain the ownership, security, and maintenance reason for every new module.
- Run `go mod tidy` only when the dependency graph intentionally changes, then review the complete diff.

## Go Tooling Dependencies

- Keep Go developer tools in the nested `tools` module so their dependency graphs do not affect the application
  module.
- Add tools with a pinned version through `go get -tool -modfile=tools/go.mod <package>@<version>`.
- Install all declared tools into the ignored project-local `bin/` directory with `make install-tools`.
- Run tools through repository Make targets backed by `bin/`; do not require or prefer a global installation.
- If the tooling graph needs normalization, run `go -C tools mod tidy`; do not run root-level `go mod tidy` with
  the tools modfile because it resolves application packages as though they belonged to the tooling module.
- Do not update a tool's transitive dependencies independently of its pinned release.

### Approved Tools

- `github.com/golangci/golangci-lint/v2/cmd/golangci-lint` v2.12.2 — approved by the owner on 2026-07-17 for the
  repository-wide Go lint gate. Its dependency graph is isolated under `tools/` because it is build-time tooling,
  not an application dependency.

### Approved Modules

- `github.com/BurntSushi/toml` — approved by the owner on 2026-07-17 for
  [Host MCP Access](design-docs/host-mcp-forwarding.md). It parses the user-authored `config.toml`
  during launcher preflight, where a hand-rolled scanner over dotted keys, inline tables, and
  multi-line strings would be a correctness liability. It is a TOML 1.1.0 parser with no transitive
  dependencies, chosen over the faster `pelletier/go-toml/v2` because it is the smaller parser and
  this code runs once per launch in the launcher's trusted computing base. The smoke module carries
  it as an indirect dependency because it imports the launcher packages that use it.

- `github.com/spf13/pflag` v1.0.10 — approved by the owner on 2026-07-18 for application CLI parsing. Its explicit
  `FlagSet.Changed` state preserves whether `--image` was supplied even when its value equals the default, without a
  manual visit over parsed flags. Both parsers disable interspersed parsing so command arguments retain the previous
  boundary.

## Container Dependencies

Pin base images and downloaded binaries as required by the owning design. Preserve checksum verification for remote
artifacts. Do not add packages merely for local convenience without documenting why they belong in the runtime image.

### Approved Runtime Packages

- `curl` — approved by the owner on 2026-07-21 for product update commands. Isolated maintenance containers use it to
  download the official OpenAI and Anthropic installers and their release assets. It is not used during normal
  session startup.

- `docker-buildx` — approved by the owner on 2026-07-17 for the project-environment image-build contract and
  repository Docker validation inside a managed session. Ubuntu 24.04 packages it as the Docker Buildx CLI plugin;
  it selects the BuildKit backend that provides Dockerfile architecture arguments required by the base image.

- Project Go toolchain — approved by the owner on 2026-07-17 for this repository's
  `.agents-safe/Dockerfile`. It copies Go 1.26.0 from the same digest-pinned image used by the session-builder stage;
  no compiler packages or system build tools are installed into the shared runtime image.

### Approved Runtime Update Channel

- OpenAI's standalone installer at `https://chatgpt.com/codex/install.sh` owns routine Codex updates in the
  `codex-safe-codex` volume. `make docker-build` invokes it after building the local image, and an explicit
  `codex-safe update` invokes it later without rebuilding. Both intentionally resolve the current official Linux
  release and rely on the installer's release checksum verification. No update runs automatically at session startup.

- Anthropic's native installer at `https://claude.ai/install.sh` owns routine Claude Code updates in the
  `codex-safe-claude` volume. `make docker-build` invokes it after the Codex updater, and an explicit
  `claude-safe update` invokes it later without rebuilding. The installer selects the current official Linux release
  and verifies its published manifest checksum. Project sessions disable background self-update and mount the volume
  read-only.

### Approved Smoke-Only Images

- `ghcr.io/astral-sh/uv:0.8.14-python3.13-bookworm-slim@sha256:5b651a2084b59293d8a9327a5b91b2779c955ebeac9bfd40f95fe91e9bc06c43`
  — approved by the owner on 2026-07-21 for the real Sysbox uv-cache tests. It provides the pinned uv/Python
  toolchain only through a temporary test project image; it must not be added to `container/Dockerfile`.

## Documentation Tooling

The Mermaid validator keeps its npm dependencies inside a Docker build under `harness/mermaid-check/`. The link
validator runs inside its own Python image under `harness/doc-links-check/` and uses only the standard library.
Neither validator requires host-side Python or npm dependencies.
