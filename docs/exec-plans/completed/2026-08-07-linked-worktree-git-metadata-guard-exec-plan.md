# Linked Worktree Git Metadata Guard

Status: completed
Created: 2026-08-07
Design: [Safe environment](../../design-docs/agents-safe.md)
Scope:
- `internal/launcher/launchplan/`, container lifecycle, and launcher fingerprint/config tests
- `tests/smoke/`
- `ARCHITECTURE.md`, `docs/design-docs/`, and `tests/smoke/README.md`

## Objective

Prevent a command running in either a primary- or linked-worktree container from pruning or rewriting another linked
worktree's administrative metadata while preserving ordinary Git writes in the selected checkout and shared
repository.

## Done Criteria

- Every session exposes `<CommonGitDir>/worktrees` read-only, including repositories with no linked worktrees yet.
- A cold launch safely materializes a missing worktree registry before Docker creates the guarded container.
- A linked-worktree session restores write access only for its own GitDir below that read-only registry.
- `git worktree prune --expire=now` in either checkout kind cannot invalidate a hidden linked worktree.
- The selected worktree can still update its index, create commits, and update shared refs.
- Every session rejects detached `git worktree add` without changing the registry, worktree list, or target path.
- Existing `.agents-safe/config.toml` files with the current three project/Git roles remain valid without rewriting.
- Configured binds, dependency caches, and tmpfs mounts cannot reopen or hide protected sibling metadata.
- Host-side worktree creation or pruning does not change the requested physical mount plan or its fingerprint.
- Focused, repository-wide, documentation, and real Sysbox gates pass.

## Current Baseline

Linked sessions mount the primary checkout read-only, the complete common `.git` directory read-write, and the
selected worktree read-write. Primary-checkout sessions expose the primary root, including the same common `.git`,
read-write. The selected checkout's `Project.GitDir` is already discovered but is not used to constrain the physical
mount plan.

Because linked worktree roots outside the selected checkout are intentionally absent, Git sees their common-directory
administrative entries as stale. In both checkout kinds, the writable common `.git` mount therefore lets
`git worktree prune` delete `.git/worktrees/<sibling>`, leaving the sibling's files in place but its `.git` pointer
unusable.

The current normalizer can also collapse an `rw` grandchild into the first `rw` ancestor without accounting for an
intermediate `ro` mount. Existing persisted project configs are authoritative and `agents-safe init` preserves them,
so adding new required serialized roles would create an avoidable migration failure.

## Implementation Decisions

- Derive protection from validated Git discovery, not TOML, and keep the existing three user-visible project/Git
  roles unchanged.
- Always derive a read-only `<CommonGitDir>/worktrees` bind for both primary and linked sessions. For a linked
  session, additionally derive a writable bind for its active `GitDir`.
- Keep the registry bind in `Plan.Mounts` whether or not its host source currently exists. On the cold-create path,
  after reuse is ruled out, materialize or verify the source as a real directory immediately before Docker create.
  Planning and active-session reuse remain host-read-only.
- Treat the nested Git bind chain as primary root `ro` -> common `.git` `rw` -> registry `ro` -> active `GitDir` `rw`
  for linked sessions. The active worktree is a separate `rw` bind, not another member of that nesting chain.
- Make `launchplan` own the guard topology invariant: a linked `GitDir` must be a canonical direct child of
  `<CommonGitDir>/worktrees`, and unexpected topology fails before Docker access.
- Normalize nested mounts against their nearest retained ancestor. The active `rw` GitDir must not be deduplicated
  across the intermediate `ro` worktrees directory.
- Resolve only host-side configured bind and dependency-cache sources through `filepath.EvalSymlinks`, and use the
  result only for protected-registry overlap validation. Preserve the configured source in `Plan.Mounts` and its
  fingerprint; do not canonicalize container-side targets. Only the launcher-derived active GitDir is exempt.
- Reject tmpfs targets that overlap the registry. Linked tmpfs targets are already confined to the separate active
  worktree root, while this explicit check prevents a regular-checkout tmpfs from hiding the protected registry.
- Intentionally disallow `git worktree add`, `remove`, `move`, and `prune` when they require registry writes inside a
  session. Worktree-topology changes are host operations; the failure must leave persistent metadata intact.
- Keep shared refs writable. Git may create a requested `-b` branch before a guarded `worktree add` reaches the
  registry and fails; this ordinary ref side effect is not a registered worktree and is outside the metadata guard.
- Keep `launchConfigSchemaVersion` at 6. The unconditional registry mount is already fingerprint input, so every
  pre-guard session gets a one-time mismatch. Registry materialization state and entry count do not affect later
  fingerprints because the same mount remains in every resolved plan.
- Treat the guard as protection from accidental or erroneous Git operations, not as isolation between mutually
  untrusted repositories. Shared refs and objects remain writable by design.

## Phases

### Phase 1: Guarded worktree-registry mount resolution

Purpose: reserve linked-worktree metadata read-only from either checkout kind while retaining writable metadata for
the selected linked worktree.
Status: done
Done when: the protected Git-topology binds are present with the required nesting for each checkout kind, and invalid
linked GitDir topology fails before Docker is contacted.

1. In `launchplan`, validate that a linked `Project.GitDir` is a canonical direct child of
   `<CommonGitDir>/worktrees`; cover rejected layouts in `internal/launcher/launchplan/plan_test.go`.
2. Always derive the read-only registry bind and derive the writable active-GitDir bind only for linked sessions,
   without adding persisted mount roles or inspecting registry existence to decide the physical plan.
3. Represent registry-source creation as cold-launch work outside the fingerprint contract. After reuse is ruled out,
   create or verify the directory immediately before Docker create and reject symlinks or non-directory entries.
4. Add focused `internal/launcher` tests for successful cold-create materialization, no mutation during reuse,
   symlink and non-directory rejection, and an actionable pre-Docker error when directory creation fails.
5. Change `normalizeLogicalMounts` to deduplicate a nested bind only against its nearest effective ancestor.
6. Add table-driven normalization cases for `rw/ro/rw`, `ro/rw/rw`, `rw/rw/ro`, exact aliases, and incompatible
   source/target mappings.
7. Update linked and regular resolution tests to assert the required Git-topology binds and access modes. Assert only
   parent-before-child order within the nested chain; do not constrain unrelated mount positions.

### Phase 2: Compatibility, bypass prevention, and reuse identity

Purpose: make the guard mandatory without breaking existing valid project configuration or session reuse semantics.
Status: done
Done when: legacy three-role configs receive the guard automatically, no configured path can punch through it, old
unsafe sessions are rejected once, and later host worktree-topology changes leave reuse identity stable.

1. Canonicalize host-side configured bind and dependency-cache sources with `filepath.EvalSymlinks` for overlap
   validation only. Preserve their exact configured sources in `Plan.Mounts`, never resolve container-side targets,
   and reject tmpfs target overlap; linked tmpfs targets remain confined to the separate active worktree root.
2. Prove existing persisted configs containing only `primary_checkout`, `common_git_dir`, and `worktree` still
   resolve successfully: linked sessions receive both derived binds and primary sessions receive the registry guard.
3. Keep the guard mounts in `Plan.Mounts` so Docker request construction and the creation fingerprint use the same
   ordered physical contract.
4. Add fingerprint tests showing the one-time identity change for every pre-guard session and stable identity across
   absent, materialized, populated, and host-pruned registry states. Exclude one-time materialization state from the
   fingerprint just as proactive tmpfs target creation is excluded.
5. Update Docker-request tests to prove the complete ordered mount list is forwarded without widening access.

### Phase 3: Real Sysbox incident regression

Purpose: reproduce the hidden-worktree failure from both checkout kinds at the real container boundary and prove it
is contained.
Status: done
Done when: prune attempts from linked and primary sessions leave hidden worktrees usable, topology creation fails
cleanly, and normal Git writes in the selected checkout still succeed.

1. Start a primary session before any linked worktree exists, assert the launcher materializes and guards the empty
   registry, then add two worktrees on the host. Resolve both active and sibling GitDir paths through Git rather than
   assuming administrative directory names.
2. Keep sibling roots absent and run `git worktree prune --expire=now` from linked and primary sessions; also prove
   elevated deletion through the guard remains read-only without attempting a remount bypass.
3. Assert on host state rather than a Git-version-specific prune exit code: the sibling remains listed, its admin
   directory remains present, and `git -C <sibling> status` succeeds.
4. Assert detached `git worktree add` fails in each session without registering a new host worktree, changing the
   protected registry, or creating its target, and document that topology changes must run on the host. Document the
   possible standalone branch side effect when callers explicitly use `-b`.
5. Preserve and extend selected-checkout probes so `status`, `add`, `commit`, shared-ref updates, and ownership still
   work after the failed topology operations.
6. Inspect each running container: common `.git` is `rw`, the registry is `ro`, only a linked session has an active
   GitDir `rw` override, and no sibling root bind exists.

### Phase 4: Durable contract and completion gates

Purpose: align architecture, configuration, and smoke documentation with the enforced runtime boundary.
Status: done
Done when: the documented contract names the derived guard and its limits, and every required validation gate passes.

1. Update `ARCHITECTURE.md` and `docs/design-docs/agents-safe.md` with both checkout-kind guard contracts, the
   unconditional registry reservation, sibling-preservation invariant, host-only topology changes, and the
   remaining shared-refs/objects limitation.
2. Update `docs/design-docs/project-launcher-configuration.md` to distinguish the three serialized logical roles from
   the two non-configurable derived physical guards and document nearest-ancestor normalization.
3. Update `tests/smoke/README.md` with primary- and linked-session regressions, rejected in-session worktree creation,
   and the inspected mount matrix.
4. Run the focused, repository-wide, documentation, and real-host gates below.
5. Record results in `Progress Notes`, set all completed phases to `done`, move the plan to `review/`, and update
   `docs/exec-plans/index.md` when implementation and all non-blocked gates are complete.

## Validation Gates

- `gofmt -l` over changed Go files prints nothing.
- `go test ./internal/launcher/launchplan ./internal/launcher` passes.
- `make lint` passes.
- `make test` passes.
- `git diff --check` passes.
- `make check-docs` passes.
- `make test-smoke-go` passes on a compatible Linux/Sysbox host; this gate includes the required image rebuild.
- The Sysbox regression proves hidden linked worktrees remain valid after ordinary and elevated prune or deletion
  attempts from both checkout kinds, topology creation fails cleanly, and each selected checkout can still commit.

## Risks and Constraints

- Nested bind behavior is a real Docker/Sysbox property; unit tests cannot replace the Sysbox smoke gate.
- The normalizer must reason about the nearest effective ancestor or it can silently remove the active `rw` override.
- A configured bind source may reach protected metadata through a symlink, so source canonicalization must precede
  overlap validation; existing lexical checks already reject conflicting or aliased target mappings.
- Git versions may differ in whether blocked prune reports success or failure; preservation of host metadata is the
  stable assertion.
- Guarding the registry intentionally makes in-session `git worktree add`, `remove`, `move`, and `prune` unavailable
  when they need registry writes. Their failure must be explicit and non-destructive.
- Host Git may delete an empty registry after the last worktree is pruned. The mount remains part of every plan, and
  the launcher rematerializes its source before the next cold create, so this does not change reuse identity.
- `--force-exec` can deliberately reuse a live container with the old fingerprint and therefore its old mount plan;
  its documented emergency semantics remain unchanged.
- Shared refs, objects, config, and common lock files stay writable. Separate clones are required for full Git
  isolation between mutually untrusted feature environments.
- Because shared refs stay writable, `git worktree add -b <branch> ...` may create `<branch>` before the guarded
  registry write fails. The guard promises no worktree registration or target/admin directory, not ref rollback.

## Out of Scope

- Removing Git from `container/Dockerfile` or wrapping/blacklisting Git subcommands.
- Replacing linked worktrees with separate clones.
- Recovering a worktree whose administrative metadata was already pruned.
- Automatically locking or unlocking host worktrees with `git worktree lock`.
- Adding new required roles to `.agents-safe/config.toml` or rewriting existing configs.
- Preventing an intentionally privileged process from attempting to alter its own mount namespace.

## Progress Notes

- 2026-08-07: Implemented the unconditional read-only registry guard for primary and linked sessions, the linked-only
  active-GitDir writable override, nearest-effective-ancestor normalization, symlink-aware overlap checks, and
  cold-create-only registry materialization with focused rejection tests.
- 2026-08-07: The real Sysbox probe showed that `git worktree add -b` creates its ordinary branch ref before the
  registry write fails. The acceptance contract was narrowed to the intended topology boundary: detached add leaves
  the worktree list, registry, and target unchanged; explicit `-b` may leave a standalone branch because shared refs
  remain writable by design.
- 2026-08-07: `gofmt -l` and `git diff --check` printed nothing; focused launcher tests, `make lint`, `make test`, and
  `make check-docs` passed. The initial sandboxed `make test` was blocked by local-socket restrictions and passed when
  rerun outside the sandbox.
- 2026-08-07: `make test-smoke-go` passed after rebuilding the image and updating both product volumes. All ordinary
  Sysbox scenarios passed, including `TestSysboxWorktreeMetadataGuard`; the separately gated credentialed acceptance
  test remained intentionally skipped because no dedicated test credentials were supplied.
