# Dependencies

Ask for explicit owner approval before adding a runtime, build, test, container, or documentation dependency.

## Go Dependencies

- Keep `go.mod` and `go.sum` synchronized.
- Prefer the standard library when it keeps the implementation clear and maintainable.
- Explain the ownership, security, and maintenance reason for every new module.
- Run `go mod tidy` only when the dependency graph intentionally changes, then review the complete diff.

## Container Dependencies

Pin base images and downloaded binaries as required by the owning design. Preserve checksum verification for remote
artifacts. Do not add packages merely for local convenience without documenting why they belong in the runtime image.

## Documentation Tooling

The Mermaid validator keeps its npm dependencies inside a Docker build under `harness/mermaid-check/`. The link
validator runs inside its own Python image under `harness/doc-links-check/` and uses only the standard library.
Neither validator requires host-side Python or npm dependencies.
