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

## Container Dependencies

Pin base images and downloaded binaries as required by the owning design. Preserve checksum verification for remote
artifacts. Do not add packages merely for local convenience without documenting why they belong in the runtime image.

## Documentation Tooling

The Mermaid validator keeps its npm dependencies inside a Docker build under `harness/mermaid-check/`. The link
validator runs inside its own Python image under `harness/doc-links-check/` and uses only the standard library.
Neither validator requires host-side Python or npm dependencies.
