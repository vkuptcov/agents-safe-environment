# Exec Plan: Project Launcher Configuration

- Status: active
- Created: 2026-07-19
- Design:
  - [`docs/design-docs/project-launcher-configuration.md`](../../design-docs/project-launcher-configuration.md)
  - [`docs/design-docs/codex-safe.md`](../../design-docs/codex-safe.md)
  - [`docs/design-docs/host-mcp-forwarding.md`](../../design-docs/host-mcp-forwarding.md)
- Scope:
  - `cmd/agents-safe/`, `cmd/codex-safe/`, and `internal/cli/`
  - `internal/launcher/projectenv/`, `internal/launcher/launchplan/`, and `internal/launcher/`
  - launcher unit tests, Sysbox smoke tests, and owning launcher documentation

## Objective

Implement the approved project-local launcher configuration. Both launchers resolve one typed config from host and
project defaults, the local TOML file, and explicit CLI overrides. The resolved config is the only source for mounts
and creation-time reuse; configured command arguments are applied to each invocation.

## Done Criteria

- `agents-safe init` writes the Dockerfile sample, typed host-specific config, and local ignore file.
- Initialization preserves existing local files and does not change the worktree-root `.gitignore`.
- Initialization does not construct a Docker launcher or contact Docker.
- Both launchers resolve `--project`, defaults, TOML, and explicitly changed flags in that order.
- Explicit CLI values affect one invocation and do not rewrite `.agents-safe/config.toml`.
- The generated config contains all required logical project/Git roles.
- A regular checkout normalizes its required logical roles to one writable bind.
- A linked worktree preserves its required read-only primary and writable Git/worktree binds.
- Every physical bind has logical-role provenance.
- Omitting a required role fails before Docker access.
- Omitting a degradable role leaves it absent and emits the documented warning on `stderr`.
- Only an exact creation-time fingerprint can reuse a running container.
- A fingerprint mismatch performs no build, stop, replacement, sidecar creation, or command execution.
- Configured Codex arguments apply on every invocation without overriding an explicit sandbox choice.
- Focused and repository-wide checks pass; real Sysbox assertions pass on a compatible host or are recorded as blocked.

## Current Baseline

- `projectenv` decodes only a flat `mounts = []` file, while `launchplan.Build` reads that file itself.
- Init writes embedded flat config and mutates the worktree-root `.gitignore`.
- The CLI passes flag defaults directly to the launcher; only `--image` tracks explicit intent.
- Host mounts, command defaults, and reuse compatibility are assembled in separate launcher paths.

## Implementation Decisions

- Use one typed resolver: defaults, TOML overlay, explicitly changed flags, then complete validation.
- A present `common.mounts` replaces the default list. Required roles fail when absent; degradable roles warn and stay
  absent.
- Validate logical roles before normalizing physical binds. Normalization may remove only a true alias and never weaken
  read-only isolation.
- Extract host identity and optional host-path discovery into a Docker-free value reused by init and launch.
- Use the versioned creation fingerprint as the sole compatibility predicate after ownership/protocol validation.
- Keep creation-time values separate from command-time argv; do not add dependencies.

## Phases

### Phase 1: Typed Resolution Foundation
Purpose: Add one testable configuration and mount-resolution path before public commands adopt it.
Status: done
Done when: typed defaults, TOML overlay, role validation, normalized binds, and configured Codex argv are unit-tested.

1. Extract a Docker-free host-environment resolver from launcher construction with filesystem and environment test
   seams.
2. Add `ProjectConfig`, TOML load/encode/validate APIs, presence-aware overlay, and deterministic serialization.
3. Build project defaults from discovered Git topology and the host environment.
4. Extend `launchplan` with role policy, degradation diagnostics, provenance, and safe normalization of resolved mounts.
5. Add configured-Codex command merging while preserving the image-owned executable and explicit sandbox selection.
6. Keep the flat reader only until Phase 2 switches production callers; cover all new behavior with focused tests.
7. Run focused package tests and `go vet` for changed Go packages.

### Phase 2: Public Config and Init Cutover
Purpose: Make both public commands and Docker requests consume the resolved typed configuration.
Status: done
Done when: init and launches use the typed resolver exclusively, with no flat config, root-ignore, or legacy mount path.

1. Resolve config after usage validation and Git discovery; apply only flags marked changed by `pflag`.
2. Make `agents-safe init` serialize typed defaults and create `.agents-safe/.gitignore` without Docker construction.
3. Pass the resolved mount plan, image intent, host-MCP policy, and command-time argv through both launchers.
4. Materialize host MCP only for a retained eligible role; print degradation diagnostics once in stable role order.
5. Delete the flat config resource/reader, root-ignore updater, Codex-home prompt policy, and independent mount
   assembly.
6. Add public CLI, init, request, and warning-order tests, then run the affected Go suites and `make test`.

### Phase 3: Creation-Time Fingerprint
Purpose: Make every running-container adoption path fail closed on a changed creation contract.
Status: to be done
Done when: a canonical fingerprint is labelled on creation and is the only post-ownership reuse predicate.

1. Add a private, versioned SHA-256 input for requested image, explicit-image intent, normalized physical binds,
   host-MCP policy, and sorted eligible endpoint addresses.
2. Compute the requested fingerprint after config and MCP resolution but before container adoption, image preparation,
   sidecar allocation, or creation.
3. Attach the label to every session create request and compare it on initial reuse, wait/reuse, concurrent winner, and
   post-exec retry.
4. Return one diagnostic mismatch error and remove the superseded Codex-home, skills, and host-MCP compatibility paths.
5. Add mutation-oriented tests proving each creation field matters, command-time fields do not, and a mismatch is inert.
6. Run focused launcher tests, `go test ./...`, `go vet`, and `make test`.

### Phase 4: Contract, Real Boundary, and Review Handoff
Purpose: Align public docs and test the serialized contract at the real Docker/Sysbox boundary.
Status: to be done
Done when: docs and smoke coverage match the shipped resolver, all available gates pass, and the plan is ready for
review.

1. Update the owning design docs, command/package documentation, and user-facing config examples.
2. Extend regular and linked-worktree smoke cases to inspect normalized mounts, role provenance, degradation, reuse
   mismatch, and command-time argv reuse.
3. Update the feature-review artifact with implementation responses and run an implementation re-review.
4. Run formatting, focused tests, `make lint`, `make test`, `make docker-build`, `make check-docs`, and
   `git diff --check`.
5. Run `make test-smoke-go` only on a host with `sysbox-runc`; otherwise record the pre-execution blocker precisely.
6. Add dated outcomes, move this plan to `review/`, and update the execution-plan index.

## Validation Gates

- `gofmt` runs on every changed Go file.
- `go test ./internal/launcher/projectenv ./internal/launcher/launchplan` passes after Phase 1.
- `go test ./internal/cli ./cmd/agents-safe ./cmd/codex-safe ./internal/launcher` passes after each public cutover.
- `go test ./...`, `go vet ./...`, `make lint`, and `make test` pass before review handoff.
- `make docker-build` passes before review handoff.
- `make check-docs` passes after documentation edits.
- `make test-smoke-go` passes on a compatible Linux host; this environment must report missing `sysbox-runc` as blocked.
- `git diff --check` passes.
- `rg -n 'LoadMounts|updateGitignore|configSampleContent' cmd internal tests` returns no legacy implementation symbol.
- `rg -n 'missingCodexHome|confirmCreateCodexHome|CodexHomePolicy' cmd internal tests` returns no prompt-policy symbol.
- `rg -n 'validateResolvedRunningUserMounts|userMountMismatchError' cmd internal tests` returns no alternate reuse path.

## Risks and Constraints

- Config is a host-specific snapshot. Missing or stale present paths fail closed and are never silently refreshed.
- `codex_home` is degradable; its presence changes the creation fingerprint and never causes host-directory creation.
- A regular checkout may collapse aliases to one bind; a linked worktree must retain the read-only primary plus writable
  common Git and worktree mounts.
- Disabled MCP and enabled MCP with no eligible endpoint both have no channel bind but remain distinct fingerprints.
- Project image preparation happens only after running-session compatibility is ruled out.
- Preserve the approved dependency set and unrelated worktree changes.

## Out of Scope

- Backward-compatible parsing or migration of the flat `mounts = []` schema.
- New configuration for bootstrap path, agent command, resource limits, timeouts, relay internals, or nested Docker.
- Stopping or replacing an incompatible active container.
- Base/session image changes or dependency additions.

## Progress Notes

- 2026-07-19: Created from the approved launcher-configuration design and current implementation baseline.
- 2026-07-19: Re-reviewed and reduced from five implementation/handoff phases to four executable increments. The
  validation contract now makes real Sysbox evidence conditional on a compatible host rather than treating its absence
  as a product failure.
- 2026-07-19: Phase 1 added the typed config overlay/encoder, Docker-free host snapshot, role-aware physical mount
  resolver with provenance, and configured Codex argv merge. Focused tests, `go test ./...`, and `go vet ./...` pass.
- 2026-07-19: Phase 2 made both public commands resolve typed config after usage validation and Git discovery, made
  launcher construction lazy, and moved init to local typed files without root-ignore edits. Docker now receives only
  the resolved physical plan; the flat reader, user-mount prompt path, and independent mount assembly are removed.
  Focused tests, `go test ./...`, `go vet ./...`, and `make test` pass.
