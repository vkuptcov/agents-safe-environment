# Exec Plan: Project Mount Configuration

- Status: active
- Created: 2026-07-18
- Design: [`docs/design-docs/project-environments.md`](../../design-docs/project-environments.md)
- Scope:
  - `internal/launcher/projectenv/` and `internal/launcher/launchplan/`
  - `cmd/agents-safe/` initialization flow
  - launcher and real Sysbox tests
  - project-environment, security, and user documentation

## Objective

Extend `agents-safe init` with a local `config.toml` that can explicitly expose additional host directories to the
project container, while preserving the launcher's canonical-path and mount-overlap boundaries.

## Done Criteria

- `agents-safe init` creates an inactive Dockerfile sample and an empty mount configuration without overwriting edits.
- The generated local files are covered by exact root `.gitignore` rules; an active project Dockerfile stays trackable.
- Each configured directory is mounted read-write at the same absolute path for new project containers.
- Invalid, missing, root, non-directory, overlapping, and unknown configuration fails before Docker launch.
- Unit tests and a real Sysbox scenario prove configuration loading and mount visibility.
- A final review removes avoidable initialization and configuration complexity before completion.

## Current Baseline

The first `init` implementation creates only `.agents-safe/Dockerfile.sample` and fails when it already exists. Launch
plans contain only Git topology mounts; no project-local file can request additional host directories.

## Implementation Decisions

- Use `mounts = []`: values are absolute host directory paths mounted read-write at the same container paths.
- Keep paths literal: no tilde, environment-variable, glob, or relative-path expansion.
- Canonicalize existing directories and reject `/`; never create a configured mount source.
- Reject configured paths that overlap managed Git mounts or another configured path; ambiguous layering is not part
  of this contract.
- Apply configuration only when creating a container. An active container keeps its immutable mount set until exit.
- Ignore only `/.agents-safe/Dockerfile.sample` and `/.agents-safe/config.toml`; do not ignore the directory or active
  `Dockerfile`.
- Make initialization additive and idempotent so rerunning the newer command upgrades output from the first version.

## Phases

### Phase 1: Durable Contract
Purpose: Define the local configuration syntax, trust boundary, and immutable-session behavior.
Status: done
Done when: design and security docs state exactly which paths are accepted and when they take effect.

1. Extend the project-environment configuration contract.
2. Document the mount boundary and local `.gitignore` behavior.

### Phase 2: Idempotent Initialization
Purpose: Generate all local project-environment inputs without destroying existing content.
Status: done
Done when: repeated `agents-safe init` calls preserve edits and leave both local files ignored.

1. Embed a default `config.toml` resource.
2. Replace one-file creation with a small idempotent initializer.
3. Add exact missing rules to the root `.gitignore`.

### Phase 3: Launch Configuration
Purpose: Add validated configured directories to new-container mount plans.
Status: done
Done when: new containers receive the configured directories and invalid configuration prevents launch.

1. Decode `config.toml` strictly in `projectenv`.
2. Canonicalize and validate configured directories.
3. Merge them into `launchplan` after the managed Git mounts.

### Phase 4: Verification and Simplification
Purpose: Prove the runtime boundary, then reduce incidental complexity found during review.
Status: in progress
Done when: unit and real-host gates pass and a final diff review finds no unnecessary abstractions or duplication.

1. Add focused initialization, parsing, launch-plan, and Docker-request tests.
2. Add a real Sysbox configured-mount scenario.
3. Review the complete diff and simplify names, control flow, helpers, and test setup.
4. Run all required validation and move this plan to review.

## Validation Gates

- `go test ./internal/launcher/projectenv ./internal/launcher/launchplan ./cmd/agents-safe` passes.
- `go test ./internal/launcher` passes.
- `make lint` passes.
- `make test` passes.
- `make test-smoke-go` passes, including the configured-mount scenario.
- `make check-docs` passes.
- `git diff --check` passes after the simplification review.

## Risks and Constraints

- A configured writable mount intentionally expands the host paths accessible to project code; the file is local and
  ignored, but code already running in the writable worktree can modify it for a later cold launch.
- Removing a mount from the file cannot revoke it from an active container; the session must end first.
- Broad parent mounts can bypass narrower read-only aliases, so overlap is rejected instead of relying on Docker mount
  order.

## Out of Scope

- Per-mount targets or read-only modes.
- File and socket mounts.
- Path expansion or automatic cache-directory discovery.
- Live mutation or replacement of an active session's mounts.

## Progress Notes

- 2026-07-18: Created after committing the first `agents-safe init` implementation as `553427c`.
- 2026-07-18: Added idempotent embedded resources, strict local mount parsing, launch-plan integration, and focused
  coverage. The simplification pass centralized path-overlap checks and stopped `.gitignore` symlinks before reads.
- 2026-07-18: `make lint`, `make test`, `make check-docs`, and focused tests pass. The real-host gate remains blocked:
  the reachable Docker daemon does not register `sysbox-runc`, so `make test-smoke-go` cannot create any managed
  smoke container. The plan stays active until that runtime proof passes.
