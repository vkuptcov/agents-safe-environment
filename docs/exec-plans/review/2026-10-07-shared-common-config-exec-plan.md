# Exec Plan: Shared Project Config (`common.toml`)

- Status: in review
- Created: 2026-10-07
- Design: `docs/design-docs/project-launcher-configuration.md`
- Scope:
  - `internal/launcher/projectenv/` (overlay loading, init, ignore file)
  - `internal/launcher/project_config.go` (layer order, shadowing warning)
  - `internal/launchcli/` (warning propagation)
  - `cmd/agents-safe/` (init output)
  - docs: `docs/design-docs/project-launcher-configuration.md`

## Objective

Let a project set portable launcher settings once in a tracked `.agents-safe/common.toml` that every worktree and
clone picks up, while the ignored `config.toml` keeps the host-specific snapshot and may override any value.

## Done Criteria

- Resolution order is defaults -> `common.toml` -> `config.toml` -> explicit CLI flags, on both the persisted-config
  and the config-less launch paths.
- `common.toml` accepts only portable keys; `mounts`, `tmpfs_mounts`, and `dependency_caches` fail before Docker
  access with a message pointing at `config.toml`.
- `config.toml` may set any key, including every key `common.toml` sets.
- A `config.toml` value that differs from a `common.toml` value emits one deterministic stderr warning per key.
- `agents-safe init` creates a missing `common.toml` with the portable defaults and never overwrites one, including
  in an already initialized worktree.
- `agents-safe init` handles the two files independently: a missing `common.toml` never triggers cache discovery,
  and an existing `config.toml` keeps its cache snapshot.
- A launch with only `common.toml` keeps the auto-discovered caches of the config-less path.
- `agents-safe init` writes only host-specific lists (`mounts`, `dependency_caches`, set `tmpfs_mounts`) into a new
  `config.toml`.
- A new `.agents-safe/.gitignore` keeps `.gitignore`, `Dockerfile`, and `common.toml` trackable.
- Existing `config.toml` files with scalar keys resolve exactly as before when no `common.toml` exists.
- `make lint`, `make test`, and `make check-docs` pass.

## Current Baseline

`projectenv.Load` overlays one optional `config.toml` on typed defaults through presence-aware `configOverlay`
pointers. `ResolveProjectConfig` uses the config-less generator when the file is absent, then applies CLI overrides.
`Initialize` encodes the full `ProjectConfig`, so every scalar is written into the ignored file. The ignore content in
code is `*\n!Dockerfile\n`, while the design doc documents an additional `!.gitignore` line.

## Implementation Decisions

- Reuse `configOverlay` for both files: one decoder, one `applyOverlay`; `common.toml` additionally rejects the
  path-bearing keys after decoding.
- Apply `common.toml` to the base config before `config.toml` in both branches of `ResolveProjectConfig`: the
  config-less generator output and the plain defaults are equally valid bases.
- `ConfigFileExists` keeps meaning local `config.toml` only: it alone selects the config-less generator (with cache
  discovery); `common.toml` presence never suppresses discovery.
- Layering warnings travel in a new `ResolvedProjectConfig.Warnings` field: `launchcli.ResolveConfig` appends them
  to the discovery warnings, so persisted-config launches surface them too.
- Fingerprint schema stays at 8: only resolved values are fingerprinted, and the layering adds no new resolved field.
- Warn only on differing values: an identical duplicate is not surprising, a differing one silently shadows the
  team setting.
- Init encodes a dedicated host-local struct instead of `ProjectConfig`, so portable keys cannot leak into
  `config.toml` by default.
- No automatic migration: existing files and existing tracked `.gitignore` files are never rewritten.

## Phases

### Phase 1: Shared Config Loading
Purpose: Read `common.toml` as a validated portable layer under `config.toml`.
Status: done
Done when: launches in any worktree apply `common.toml` values, and `config.toml` overrides them with a warning.

1. Add `CommonConfigName` and a regular-file locator shared with `config.toml`.
2. Split `Load` into a reusable `overlay(path)` step; add `LoadLayers` returning the config and shadowed keys.
3. Reject `common.mounts`, `common.tmpfs_mounts`, and `common.dependency_caches` in `common.toml`.
4. Apply the layers in `ResolveProjectConfig` on both the persisted and config-less paths.
5. Add `ResolvedProjectConfig.Warnings`; append it to the discovery warnings in `launchcli.ResolveConfig`.
6. Add tests: three-level precedence, CLI over both files, rejected keys, differing/identical shadowing, absent
   `common.toml` parity.
7. Add tests: shadowing warning reaches stderr when `config.toml` exists; only-`common.toml` launch keeps both the
   shared values and the discovered caches.

### Phase 2: Initialization
Purpose: Make `init` produce the tracked/local split by default.
Status: done
Done when: `init` in a fresh worktree yields a tracked `common.toml` and a mounts-only `config.toml`.

1. Check presence of `common.toml` and `config.toml` independently; return a created flag for each.
2. Encode `common.toml` from portable defaults when missing, without running cache discovery; never overwrite it.
3. Encode `config.toml` from a host-local struct with only `mounts` and `dependency_caches`; discover caches only
   when this file is missing.
4. Change the ignore content to `*`, `!.gitignore`, `!Dockerfile`, `!common.toml`.
5. Report each created file separately in `agents-safe init` output.
6. Add tests for all four presence combinations: exact new contents, untouched existing contents and caches.

### Phase 3: Documentation
Purpose: Keep the configuration contract authoritative.
Status: done
Done when: the design doc describes both files, the order, the warning, and init output.

1. Update `project-launcher-configuration.md`.
2. Update `ARCHITECTURE.md` data-boundary bullet for the tracked file.

## Validation Gates

- `go test ./internal/launcher/projectenv/... ./internal/launcher/... ./cmd/...` passes.
- `make lint` and `make test` pass.
- `make check-docs` passes.
- Manual: `agents-safe init` in a scratch worktree writes the documented `common.toml`, `config.toml`, and
  `.gitignore`; `git status` lists `common.toml` as untracked-but-trackable.

## Risks and Constraints

- Tracked `common.toml` is project-controlled input, like the tracked `Dockerfile`; it can only select values the
  launcher already validates and cannot add host paths.
- Existing worktrees with a full `config.toml` shadow `common.toml`; the warning makes this visible, and the user
  removes the local key to adopt the shared value.
- Existing tracked `.agents-safe/.gitignore` files need a manual `!common.toml` line.

## Out of Scope

- Host-wide or per-user config layers.
- Portable mount or dependency-cache declarations.
- Rewriting or migrating existing `config.toml` or `.gitignore` files.

## Progress Notes

- 2026-10-07: Plan created; design approved in chat.
- 2026-10-07: Implemented all three phases. Deviations: init also writes a set `common.tmpfs_mounts` into
  `config.toml` (it is path-bearing like the other host lists); `Load` remains as a thin wrapper over
  `LoadLayers`, and `Encode` still writes the complete config used by smoke fixtures. `make lint`, `make test`,
  and `make check-docs` pass; manual `init` in a scratch repo produced the documented files. `make test-smoke-go`
  was not run.
- 2026-10-07: Simplification pass: `CommonConfig` now embeds `PortableCommonConfig` and `HostCommonConfig`, the
  single source for the common.toml key rule and both init encoders; override warnings are derived generically
  from decoded values and sorted; exported `Load` removed in favor of `LoadLayers`.
- 2026-10-07: Addressed implementation review F-001/F-002
  (`docs/reviews/feature-review/2026-10-07-shared-common-config-implementation-review.md`): both layers now reject
  keys not spelled in lowercase schema form, so case-insensitive decoder matching can no longer bypass the
  `common.toml` host-key rule or split override-warning keys.
