# Exec Plan: Internal Naming and Container Terminology

- Status: completed
- Created: 2026-07-16
- Design: [`agents-safe.md`](../../design-docs/agents-safe.md)
- Scope:
  - `internal/`, `cmd/`, and `tests/smoke/`
  - `README.md`, `ARCHITECTURE.md`, and current design documentation

## Objective

Make package boundaries, type names, file roles, and container terminology understandable during code review without
requiring readers to reconstruct intent from call sites.

## Done Criteria

- Ambiguous internal types and helpers use names that state their launcher, container, or session role.
- Every package README identifies its boundary and briefly describes each non-test Go file.
- The primary Sysbox-managed environment is called the container; `Sysbox container` is used only when distinction
  from nested containers is necessary.
- Runtime behavior and Docker arguments remain unchanged.
- Required Go and documentation checks pass.

## Current Baseline

The code uses broad names such as `Plan`, `Docker`, `App`, `runtimePaths`, and `RunnerConfig`. Documentation commonly
calls the primary Sysbox-managed environment the `outer container`, which makes the topology harder to scan.

## Implementation Decisions

- Keep package directory names when package context makes file ownership clear; explain that ownership in its README.
- Rename identifiers only when package qualification still leaves their role ambiguous.
- Treat this as a semantic-preserving refactor; do not change lifecycle, mounts, or security boundaries.

## Phases

### Phase 1: Package Navigation
Purpose: Make every internal package and non-test file understandable from its README.
Status: done
Done when: each package README states its boundary and lists every non-test Go file with a short purpose.

1. Complete the package README set.
2. Clarify that `internal/container/` contains code executed inside and configuring the Sysbox container.
3. Keep README file lists synchronized with any file renames.

### Phase 2: Identifier and File Naming
Purpose: Replace ambiguous names with reviewer-readable container, launcher, and session terminology.
Status: done
Done when: key types, helpers, and files state what they configure or execute without relying on implementation detail.

1. Rename the launcher plan, bind mount, Docker launcher, and CLI dependency types.
2. Rename container account, path, daemon-process, and user-filesystem helpers.
3. Rename session command configuration and terminal detection helpers.
4. Update tests and package README file lists.

### Phase 3: Container Terminology
Purpose: Use one name for the primary Sysbox-managed container across active code and durable documentation.
Status: done
Done when: active code and governing docs use `container` by default and qualify nested containers explicitly.

1. Update code comments, diagnostics, test names, and smoke-harness helper names.
2. Update `README.md`, `ARCHITECTURE.md`, and owning design docs.
3. Leave historical review reports unchanged unless they describe a current contract incorrectly.

### Phase 4: Validation and Handoff
Purpose: Prove the naming refactor preserves behavior and documentation integrity.
Status: done
Done when: all validation gates pass and the plan is moved to review.

1. Format changed Go files.
2. Run the repository Go gate and documentation gate.
3. Move this plan to `review/` and update the plan index.

## Validation Gates

- `GOCACHE=/tmp/agents-safe-go-build make test` passes.
- `make check-docs` passes.
- `git diff --check` passes.
- `rg -n -i "outer[- ]container" internal cmd tests README.md ARCHITECTURE.md docs/design-docs` returns no current
  terminology matches.
- Every directory containing non-test Go files under `internal/` contains a `README.md` listing those files.

## Risks and Constraints

- Broad terminology replacement can obscure the distinction from nested containers; qualify those references explicitly.
- Do not rename Docker labels, environment variables, executable names, or persisted compatibility values.
- Preserve the user's existing uncommitted README and comment changes as part of this work.

## Out of Scope

- Runtime behavior changes.
- Package-boundary redesign.
- Historical review-report rewriting.
- Public CLI flag or output changes unrelated to terminology.

## Progress Notes

- 2026-07-16: Started after reviewer feedback that package, type, file, and primary-container terminology was ambiguous.
- 2026-07-16: Renamed ambiguous launcher, container, session, terminal, and test-support entities and files.
- 2026-07-16: Added package boundaries and non-test file purposes to every `internal/` package README.
- 2026-07-16: Replaced primary-environment `outer container` terminology with `container` and qualified nested Docker.
- 2026-07-16: Split launcher responsibilities by file while retaining the Docker CLI transport.
