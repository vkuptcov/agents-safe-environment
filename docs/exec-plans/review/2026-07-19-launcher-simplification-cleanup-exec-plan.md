# Exec Plan: Launcher Simplification Cleanup

- Status: in review
- Created: 2026-07-19
- Design: `docs/design-docs/project-launcher-configuration.md`
- Scope:
  - `cmd/agents-safe/`, `cmd/codex-safe/`, `internal/cli/`, `internal/launchcli/`
  - `internal/launcher/project_config.go`, `internal/launcher/codex.go`
  - launcher configuration docs and technical-debt tracker

## Objective

Remove redundant launcher wiring, validation, host discovery, and role state while preserving CLI behavior and the
resolved container contract.

## Done Criteria

- Both public commands use one default-image source and one lazy launcher dependency path.
- Project configuration is not validated twice at the same resolution boundary.
- Production-unused Codex command compatibility code is removed with its obsolete tests.
- The documented typed schema includes `MountRole`.
- One resolved canonical host home is reused by configuration and launcher construction.
- The launch plan stores host-MCP capability without duplicating all logical roles.
- TD-3 and TD-4 are removed after their acceptance criteria pass.
- Required Go and documentation gates pass.

## Current Baseline

The CLI carries the default image both in `cli.Config` and command-local resolver closures, and tests may inject either
`Dependencies.Launcher` or `Dependencies.NewLauncher`. `ResolveProjectConfig` validates the same resolved config that
`launchplan.Resolve` immediately validates again. `DefaultCodexCommand` has no production caller. The design-doc schema
still shows the pre-`MountRole` field type.

## Implementation Decisions

- Keep lazy launcher construction so usage and project-config errors still occur before launcher initialization.
- Pass `Config.DefaultImage` to the resolver dependency; bind `launchcli.ResolveConfig` directly in production.
- Keep full validation in `launchplan.Resolve`; remove only the immediately redundant caller validation.
- Preserve lazy launcher construction while passing it the already-resolved canonical host home.
- Preserve filesystem-role provenance while replacing the general plan role set with explicit host-MCP capability.

## Phases

### Phase 1: CLI Wiring Cleanup
Purpose: Establish one default-image source and one launcher injection path.
Status: done
Done when: both commands bind the shared resolver directly and every caller uses lazy launcher construction.

1. Extend the resolver dependency signature with the configured default image.
2. Remove the direct `Launcher` dependency field and update tests to use `NewLauncher`.
3. Remove command-local resolver closures.

### Phase 2: Resolver and Codex Cleanup
Purpose: Remove redundant validation and production-unused compatibility code.
Status: done
Done when: `launchplan.Resolve` owns final validation and no production-unused Codex wrapper remains.

1. Remove the duplicate `projectenv.Validate` call.
2. Remove `DefaultCodexCommand` and its obsolete tests.
3. Keep `CodexCommand` behavior and coverage unchanged.

### Phase 3: Durable Documentation
Purpose: Keep the schema and deferred-work record aligned with the implementation boundary.
Status: done
Done when: the design doc names `MountRole` and both larger simplifications have actionable debt entries.

1. Update the typed schema example.
2. Add detailed host-snapshot and plan-role debt entries with concrete impact and suggested fixes.

### Phase 4: Verification and Handoff
Purpose: Prove behavior is unchanged and place the plan in the correct lifecycle state.
Status: done
Done when: focused and repository gates pass and this plan is moved to `review/`.

1. Run `gofmt` on changed Go files.
2. Run focused CLI and launcher tests.
3. Run `make lint test check-docs` and `git diff --check`.
4. Move this plan to `docs/exec-plans/review/` and update the plan index.

### Phase 5: Close Host and Role Debt
Purpose: Use one host snapshot and one representation for each kind of launch-plan state.
Status: done
Done when: launcher construction reuses the resolved host home, the plan has explicit host-MCP capability, and TD-3
and TD-4 are removed.

1. Carry canonical host home through `cli.ResolvedConfig` into the lazy launcher factory.
2. Make `NewDockerLauncher` accept the resolved home instead of rediscovering host identity.
3. Replace `Plan.Roles` and `HasRole` with explicit host-MCP capability state.
4. Update focused tests and remove the completed debt entries.
5. Rerun all validation gates and return this plan to `review/`.

## Validation Gates

- `go test ./cmd/agents-safe ./cmd/codex-safe ./internal/cli ./internal/launchcli ./internal/launcher` passes.
- `make lint test check-docs` passes.
- `git diff --check` reports no whitespace errors.
- `rg -n "DefaultCodexCommand|Launcher Launcher" internal cmd` returns no production compatibility seam.
- `rg -n "Plan\.Roles|HasRole\(|containsRole\(" internal tests` returns no duplicate general role set.

## Risks and Constraints

- Launcher construction must remain lazy so help and usage failures do not perform host or Docker preflight.
- Explicit `--image` behavior and config-file precedence must remain unchanged.
- Technical-debt entries must describe bounded future changes, not silently broaden this cleanup.

## Out of Scope

- Real Sysbox behavior; no mount or container runtime semantics change in this cleanup.

## Progress Notes

- 2026-07-19: Started from the accepted simplification review; larger structural items are intentionally deferred.
- 2026-07-19: Implemented items 1-5, recorded deferred items as TD-3 and TD-4, and passed all validation gates.
- 2026-07-19: Owner requested immediate closure of TD-3 and TD-4; reopened the plan for Phase 5.
- 2026-07-19: Reused the resolved canonical home, replaced the duplicate role set with explicit host-MCP state,
  removed TD-3 and TD-4, and passed focused and repository-wide gates.
