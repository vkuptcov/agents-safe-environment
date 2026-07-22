# Exec Plan: Container Diagnostic Tools

- Status: in review
- Created: 2026-07-22
- Design: `docs/design-docs/agents-safe.md`
- Scope:
  - `container/Dockerfile`
  - `tests/smoke/`
  - Runtime and dependency documentation

## Objective

Make common repository, process, network, artifact, and SQLite diagnostics available in every managed agent session.

## Done Criteria

- The shared session image contains the approved diagnostic commands.
- The Dockerfile explains why each package group belongs in the runtime image.
- The real Sysbox smoke test reports any missing diagnostic command by name.
- Runtime and dependency documentation describe the installed tooling.

## Current Baseline

The image supplies Git, Ripgrep, Make, Docker, Curl, and basic interactive shell tools. It does not provide structured
JSON inspection, process and socket inspection, network probes, archive inspection, or a SQLite client.

## Implementation Decisions

- Use Ubuntu packages so the existing pinned base image owns architecture selection and package compatibility.
- Install only non-interactive command-line tools useful across repository types; keep project linters and language
  toolchains in project-specific environments.
- Test command availability as one smoke contract while preserving the exact missing-command list in diagnostics.

## Phases

### Phase 1: Image and Contract
Purpose: Add the diagnostic commands and document their runtime purpose.
Status: done
Done when: the Dockerfile and durable documentation agree on the installed package set.

1. Add the Ubuntu packages with comments grouped by responsibility.
2. Update the safe-environment design, README, and dependency approval record.

### Phase 2: Validation
Purpose: Prove every promised command exists in a real managed session.
Status: done
Done when: automated checks and the required image and Sysbox gates pass, or an environment blocker is recorded.

1. Extend the environment smoke report and assertions.
2. Run formatting, tests, documentation validation, image build, and the real Sysbox smoke gate.

## Validation Gates

- `gofmt -w tests/smoke/sysbox_linked_worktree_test.go` leaves the changed Go test formatted.
- `make test` passes.
- `make check-docs` passes.
- `make docker-build` builds the image and refreshes both product volumes.
- `make test-smoke-go` proves all commands are available in a real Sysbox session.
- `git diff --check` reports no whitespace errors.

## Risks and Constraints

- The packages increase the shared image size and inherit Ubuntu security-update cadence.
- Image and smoke validation require Docker, network access, and a compatible Sysbox host.

## Out of Scope

- Project-specific language toolchains and linters.
- Interactive-only tools such as fuzzy finders and Git diff pagers.
- Replacing Ripgrep file discovery with `fd`.

## Progress Notes

- 2026-07-22: Owner approved adding the diagnostic tool set to the shared image.
- 2026-07-22: Added the packages, durable contract, dependency record, and a smoke assertion that preserves the names
  of missing commands. `make test`, `make check-docs`, shell syntax checks, Go formatting, and `git diff --check`
  passed.
- 2026-07-22: `make docker-build` could not complete because `archive.ubuntu.com` timed out from both the default
  BuildKit network and a host-network diagnostic build. Direct HTTP and HTTPS probes from the host also timed out;
  `security.ubuntu.com` remained reachable. `make test-smoke-go` was not run because it depends on the blocked image
  build. This is an environment blocker before the changed image can execute, not a package-resolution failure.
