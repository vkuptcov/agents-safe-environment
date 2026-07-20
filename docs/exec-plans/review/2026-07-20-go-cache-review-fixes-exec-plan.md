# Exec Plan: Go Cache Implementation Review Fixes

- Status: in review
- Created: 2026-07-20
- Design: [`docs/design-docs/host-backed-dependency-caches.md`](../../design-docs/host-backed-dependency-caches.md)
- Scope:
  - `internal/launcher/` session adoption readiness
  - `internal/launchcli/` explicit empty cache snapshots
  - host-backed cache design and implementation review artifact

## Objective

Close the open implementation-review findings before merge: make concurrent cold-session adoption safe, keep the
shipped cache contract Go-only, and persist an explicit empty snapshot for `--host-caches=none`.

## Done Criteria

- Every running-container adoption path completes root readiness before unprivileged exec.
- A matching concurrent-create conflict has a focused ordering regression test.
- Current-contract and test-plan wording names only the implemented Go cache profiles.
- `--host-caches=none` generates `dependency_caches = []` without probing Go.
- The source review report records all findings as fixed with passing verification.

## Current Baseline

The successful container creator waits for readiness, but running-container adoption paths return directly. The broad
design retains several current-tense uv, Maven, and Gradle statements despite strict Go-only parsing. A `none` selection
returns a nil cache slice, which the TOML encoder omits instead of serializing as an empty array.

## Implementation Decisions

- Adoption readiness: run the existing root-only readiness request before every adopted container is returned.
- Explicit empty snapshot: return a non-nil empty cache slice for `none`; retain nil for auto discovery with no result.
- Contract scope: keep deferred ecosystem analysis in alternatives, but make current contract and tests Go-only.

## Phases

### Phase 1: Runtime and Serialization Fixes
Purpose: Remove the lifecycle race and persist the explicit opt-out.
Status: done
Done when: focused tests prove readiness ordering and empty-list encoding.

1. Add readiness to every running-container adoption path.
2. Add a matching name-conflict regression test and update established-reuse expectations.
3. Return a non-nil empty cache snapshot for `none` and cover its TOML encoding.
4. Run focused Go tests and `gofmt`.

### Phase 2: Contract and Review Closure
Purpose: Synchronize durable documentation and record the resolved findings.
Status: done
Done when: the Go-only contract is consistent and the review responses cite passing gates.

1. Remove current-tense deferred-profile claims from the contract and test plan.
2. Update the implementation review with responses for F-001 through F-003.
3. Run repository-wide, documentation, and real Sysbox gates.
4. Move this plan to `review/` and update the plan index.

## Validation Gates

- `go test ./internal/launcher ./internal/launchcli ./internal/launcher/projectenv` passes.
- `make lint` passes with writable Go and linter caches when required by the sandbox.
- `make test` passes.
- `make check-docs` passes.
- `make test-smoke-go` passes on the available Sysbox host.
- `git diff --check` passes.

## Risks and Constraints

- Readiness must stay root-only and must not register a managed command or alter idle state.
- An adopted container can already be stopping; the existing wrapper retry path remains authoritative after readiness.
- No dependency changes are allowed.

## Out of Scope

- Implementing uv, Maven, or Gradle cache profiles.
- Changing the session wire protocol or idle-timeout policy.
- Cache migration, cleanup, quotas, or nested-container propagation.

## Progress Notes

- 2026-07-20: Created from F-001 through F-003 in the implementation review.
- 2026-07-20: Fixed all findings. Focused tests, lint, `make test`, `make check-docs`, manual init verification, and
  `make test-smoke-go` pass; moved to review for owner acceptance.
