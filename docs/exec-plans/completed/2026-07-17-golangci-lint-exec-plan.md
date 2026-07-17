# Exec Plan: GolangCI-Lint Integration

- Status: completed
- Created: 2026-07-17
- Scope:
  - `tools/`
  - `.golangci.yml`
  - `Makefile`
  - `docs/testing.md`, `docs/dependencies.md`, and `docs/makefile-reference.md`

## Objective

Add a reproducible GolangCI-Lint gate without adding tool dependencies to the application module graph.

## Done Criteria

- GolangCI-Lint is version-pinned in an isolated module under `tools/`.
- `make install-tools` compiles every declared tool into the ignored project-local `bin/` directory.
- `make lint` reports issues without rewriting files, while `make lint-n-fix` applies supported fixes in place.
- Repository documentation identifies the lint gate and the isolated dependency-management contract.
- The lint, test, documentation, and whitespace gates pass.

## Current Baseline

The repository runs `go test` and `go vet` for the application and smoke modules, but has no aggregate Go linter,
lint configuration, or separately managed Go tooling module.

## Implementation Decisions

- Use the Go 1.24+ `tool` directive in `tools/go.mod`; this repository targets Go 1.26.
- Pin GolangCI-Lint v2.12.2 and do not modify its transitive dependencies independently.
- Start from GolangCI-Lint's standard set and add commonly useful low-noise checks.
- Keep `make test` unchanged; expose lint as a separate completion gate so focused test workflows stay focused.
- Keep automatic fixes opt-in through a separate target; the read-only lint target remains the completion gate.
- Install the `tool` meta-pattern into `bin/` and make lint targets execute the compiled local binary.

## Phases

### Phase 1: Isolated Tool Module
Purpose: Make the linter version reproducible without changing the application dependency graph.
Status: done
Done when: `tools/go.mod` and `tools/go.sum` own the complete GolangCI-Lint dependency graph.

1. Create the nested tools module.
2. Add the pinned GolangCI-Lint tool dependency.
3. Confirm the root `go.mod` and `go.sum` are unchanged.

### Phase 2: Lint Gate
Purpose: Establish a useful repository-wide static-analysis baseline.
Status: done
Done when: separate read-only and opt-in fixing targets apply the documented v2 configuration to the application
module.

1. Add the GolangCI-Lint v2 configuration.
2. Add the project-local tool installation target.
3. Add read-only and auto-fix Makefile targets that execute the compiled binary.
4. Resolve actionable baseline findings without broad exclusions.

### Phase 3: Documentation and Validation
Purpose: Make the dependency and completion-gate contracts discoverable and proven.
Status: done
Done when: maintainers can find the lint command and all required validation gates pass.

1. Update dependency, testing, and Makefile documentation.
2. Run lint, tests, documentation validation, and whitespace validation.
3. Record results and move this plan to review.

## Validation Gates

- `bin/golangci-lint config verify` accepts `.golangci.yml` after tool installation.
- `make install-tools` creates `bin/golangci-lint` from the pinned tools module.
- `make lint` passes.
- `make lint-n-fix` passes on the clean baseline without changing any Go file.
- `make test` passes.
- `make check-docs` passes.
- `git diff --check` passes.
- `git diff -- go.mod go.sum` is empty.

## Risks and Constraints

- GolangCI-Lint recommends release binaries over source builds; the approved repository contract explicitly requires
  the isolated Go tools-module pattern.
- The nested tool module has a large transitive graph; only the top-level GolangCI-Lint version is maintained here.
- Avoid high-noise style and complexity checks in the initial baseline.

## Out of Scope

- CI-provider configuration.
- Real Sysbox smoke testing; this change does not affect runtime boundaries.

## Progress Notes

- 2026-07-17: Owner approved the GolangCI-Lint dependency and isolated tools-module approach.
- 2026-07-17: Added v2.12.2 under `tools/`, enabled the standard set plus focused low-noise linters, and resolved
  the eight actionable findings left after applying GolangCI-Lint's documented false-positive presets.
- 2026-07-17: `make lint` and `make test` passed in the repository's pinned Go 1.26 container because the host shell
  has no Go binary. `make check-docs`, `git diff --check`, and root-module isolation checks passed on the host.
- 2026-07-17: Replaced one stale link to an absent ignored feature-review artifact that blocked `make check-docs`.
- 2026-07-17: Added the owner-requested `make lint-n-fix` opt-in rewrite mode while keeping `make lint` read-only.
- 2026-07-17: Added `make install-tools`; lint targets now execute the compiled project-local binary.
- 2026-07-17: Verified v2.12.2 installation, incremental no-op behavior, compiled-binary lint/fix execution, and the
  full test gate in the pinned Go 1.26 environment.
- 2026-07-17: Owner accepted the completed lint integration and moved this plan to `completed/`.
