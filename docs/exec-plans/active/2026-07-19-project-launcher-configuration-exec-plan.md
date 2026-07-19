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
  - launcher unit tests, real Sysbox smoke tests, and user-facing launcher documentation
  - owning launcher, safe-environment, and host-MCP design documentation

## Objective

Implement the approved project-local launcher configuration contract. Both launchers must resolve one typed config
from host/project defaults, the local TOML file, and explicit CLI overrides; use it as the authoritative mount and
creation-time contract; and apply launcher arguments independently on every command.

## Done Criteria

- `agents-safe init` writes the design-defined Dockerfile sample, typed host-specific config, and local ignore file.
- `agents-safe init` preserves existing files.
- `agents-safe init` neither constructs a Docker launcher nor contacts Docker.
- `agents-safe init` does not modify the worktree-root `.gitignore`.
- Both binaries use the resolution order `--project` -> typed defaults -> TOML -> explicit flags -> validation.
- `.agents-safe/config.toml` shows the effective image, host-MCP setting, logical mounts, and launcher arguments.
- Explicit CLI values override project defaults for one invocation without changing the file.
- Every config contains the required `worktree`, `primary_checkout`, and `common_git_dir` logical roles.
- A regular checkout normalizes its three required logical roles to one writable physical mount.
- A linked worktree materializes its three required logical roles as three physical mounts in safe order.
- Every physical bind mount is traceable to one or more validated logical roles.
- Removing any required role fails before Docker access.
- Missing degradable roles remain absent and emit the specified launcher-aware warning on `stderr`.
- A running container is reused only when its complete creation-time fingerprint matches.
- A fingerprint mismatch performs no build, stop, replacement, or command execution.
- Configured Codex arguments apply to every invocation.
- An explicit invocation sandbox choice suppresses the configured launcher-default sandbox pair.
- Focused tests, repository-wide Go gates, documentation checks, and real Sysbox mount/reuse checks pass.

## Current Baseline

The shipped implementation is the earlier flat mount feature:

- `internal/launcher/projectenv/config.go` decodes only `mounts = []`, and `launchplan.Build` reads that file itself.
- `projectenv.Initialize` embeds a static `config.toml` and appends ignore rules to the worktree-root `.gitignore`.
- `internal/cli.Run` builds the command before project discovery and passes raw flag defaults directly to the
  launcher; only explicit `--image` intent is tracked with `FlagSet.Changed`.
- host Git config, Git topology, Codex home, personal skills, additional mounts, and the MCP channel are assembled in
  separate code paths rather than one role-aware creation plan.
- reuse validates ownership plus separate Codex-home, skills, and host-MCP labels; no single fingerprint covers image,
  mounts, host-MCP policy, and endpoint identity.
- `launcher.DefaultCodexCommand` owns the correct explicit-sandbox suppression rule, but its default argv is not
  loaded from project configuration.

## Implementation Decisions

- **One resolver:** after config-independent usage validation and Git discovery, build a fresh typed default config,
  overlay TOML with field-presence semantics, apply only flags whose `pflag.FlagSet.Changed` bit is set, then validate
  the complete config.
- **Authoritative snapshot:** when `common.mounts` is present it replaces the entire default slice. Never merge it or
  silently restore removed/stale roles. Reject a missing required role and retain a missing degradable role.
- **Stable logical topology:** `worktree`, `primary_checkout`, and `common_git_dir` are always generated and required.
  For a regular checkout, `worktree` and `primary_checkout` are the same writable path and `common_git_dir` is their
  nested writable `.git`; for a linked worktree, the writable worktree, read-only primary checkout, and nested
  writable common Git directory are distinct logical requirements. `host_git_config`, `codex_home`,
  `personal_skills`, and `host_mcp_channel` are degradable. Their absence prints the owning design's exact warning
  before Docker access; `codex_home` absence gives Codex ephemeral container-local state rather than blocking either
  launcher.
- **Minimal physical mounts:** validate the complete logical role set first, then normalize exact aliases and nested
  mounts. A retained parent may satisfy a nested role only when it exposes the same subtree with the required mode;
  normalization must never weaken `ro` to `rw`. Docker requests and compatibility fingerprints use the resulting
  physical mount list, while diagnostics retain the logical-role provenance.
- **One reuse predicate:** presence or absence of `codex_home`, personal skills, and host MCP participates in the
  creation-time fingerprint. Their labels remain diagnostic metadata and never select an alternate compatibility or
  active-session error path; an absent-to-present Codex-home change uses the generic fingerprint mismatch.
- **Single mount source:** convert validated config roles into the normalized `launchplan.Plan`; Docker request code
  must not independently prepend host Git, Codex-home, skills, or additional mounts.
- **Logical MCP role:** keep `runtime://host-mcp-channel` in config, but materialize a physical channel mount only when
  forwarding is enabled and eligible endpoints exist. Fingerprint host-MCP policy and endpoint identity; exclude the
  logical placeholder and random per-session generation path from the physical mount list.
- **Separate lifecycle state:** image selection intent, normalized creation mounts, `no_host_mcp`, and effective MCP
  endpoint identity are creation-time data. Codex and agent argv are command-time data.
- **Launcher-aware Codex-home warning:** when config removes a default `codex_home` role, both binaries warn because
  the container loses persistent Codex state. When the host default never existed, only `codex-safe` warns and uses
  ephemeral state; `agents-safe` starts without a Codex-specific warning.
- **Fingerprint before build:** compare the requested creation fingerprint before project-image preparation so active
  reuse never triggers a build. Hash the requested image reference and explicit-image intent, not a built image ID.
- **No hidden shell policy:** configured and invocation arguments remain separate argv elements. Keep the absolute
  image-owned Codex executable and the existing sandbox-selection detection.
- **No new dependency:** use the existing BurntSushi TOML and `pflag` dependencies plus the Go standard library.

## Resolution and Runtime Flow

```text
parse bootstrap/launcher flags
  -> reject a missing agents-safe command as a usage error
  -> discover --project worktree
  -> build fresh typed project/host defaults
  -> overlay .agents-safe/config.toml
  -> apply explicitly changed flags
  -> validate all sections and the complete logical role set
  -> normalize aliases/nesting into the minimal physical launch plan
  -> print degradation warnings
  -> resolve effective host-MCP endpoints and creation fingerprint
  -> inspect and either reuse exactly, reject mismatch, or create cold
  -> merge launcher-specific command argv and execute through docker exec
```

Help, flag-syntax errors, and `agents-safe`'s config-independent missing-command error return before discovery. For all
launch attempts, `--project` remains the only value needed before config lookup; only config-dependent argv merging
runs after resolution.

## Phases

### Phase 1: Additive Typed Config Foundation
Purpose: Introduce the typed schema and overlay engine without breaking the existing flat runtime path.
Status: to be done
Done when: typed config round-trips and validates in focused tests while all existing callers still compile and pass.

1. In `internal/launcher/projectenv/config.go`, add the exported `ProjectConfig`, `CommonConfig`, `CodexConfig`,
   `AgentsConfig`, and `MountConfig` types from the design plus constants for every supported mount role and the
   logical MCP source.
2. Add APIs equivalent to `Load(projectRoot, defaults)`, `Encode(config, writer)`, and structural `Validate(config)`.
   Decode through presence-aware overlay types so omitted values retain defaults, a present mounts array replaces the
   whole slice, unknown keys fail, and default slices are cloned before overlay.
3. Reject empty images, malformed/unsafe argv, unsupported or incomplete mount records, invalid types, and malformed
   logical sources without partially mutating defaults. Leave host-path, role-identity, mode, and overlap checks to
   the resolved-plan API added in Phase 2.
4. Keep `LoadMounts`, the embedded flat `config.toml`, current `Initialize`, and root-ignore behavior temporarily as
   compatibility code. Mark them for deletion in the atomic Phase 3 cutover; do not expose typed output publicly yet.
5. Add focused `projectenv` tests for deterministic encoding, all three sections, scalar omission, explicit false,
   omitted versus present-empty mounts, whole-list replacement, unknown nested keys, invalid records, and clone safety.
6. Run the Phase 1 focused gate and `go test ./...`; the phase is not done if an existing package stops compiling.

### Phase 2: Additive Defaults and Resolved Mount Plan
Purpose: Build and test the new resolution primitives before switching either public command to them.
Status: to be done
Done when: pure defaults, role classification, normalized mount planning, and command merging are testable alongside
the unchanged public runtime.

1. Extend `internal/launcher/host_environment.go` with a Docker-independent `HostEnvironment` resolver for host
   identity, canonical home, Git config, and optional Codex-home/personal-skills state. Reuse the resolved value in
   `NewDockerLauncher` and project-default generation; keep OS user, environment, and filesystem seams testable.
2. Add `internal/launcher/project_config.go` with one launcher-neutral default builder that accepts the discovered Git
   project and `HostEnvironment`. Always emit `worktree`, `primary_checkout`, and `common_git_dir`. For a regular
   checkout, emit worktree and primary as the same `rw` path and nested `.git` as `rw`; for a linked worktree, emit
   distinct `worktree:rw`, `primary_checkout:ro`, and `common_git_dir:rw` paths. Emit available degradable roles with
   the design-defined paths, modes, comments, and logical MCP entry.
3. Add a role-policy registry used by validation and diagnostics: `worktree`, `primary_checkout`, and
   `common_git_dir` are always required; `host_git_config`, `codex_home`, `personal_skills`, and `host_mcp_channel`
   are degradable; `additional` entries are user-owned. Validate each required role against the paths and modes
   derived from `gitproject.Project` before normalization.
4. Add a resolved-plan API alongside the current `launchplan.Build`. It accepts the resolved common config and
   host-derived baseline, validates managed role identity/mode/path, returns required-role errors and ordered
   degradation diagnostics, and normalizes the logical roles into physical `BindMount` values without reading TOML.
   Retain role provenance separately so diagnostics and inspect tests can trace every physical bind to its source
   role or roles.
5. Normalize exact source/target/mode aliases and remove a nested role only when its parent mapping already exposes
   the same subtree at the required mode. Validate multiple `additional` entries, conflicting duplicate targets,
   stale present paths, unsafe writable/read-only aliases, and linked-worktree parent-before-child ordering. Treat the
   logical MCP role separately from filesystem sources and preserve comments outside runtime/Docker structures.
6. Add `CodexCommand(configured, invocation)` alongside `DefaultCodexCommand`. Preserve the absolute executable, argv
   ordering, no-shell behavior, and suppression of only the configured default sandbox choice when invocation argv
   explicitly selects a policy.
7. Add focused host-environment, default-builder, launch-plan, warning-order, and Codex-command tests. Prove
   `NewDockerLauncher` and the default builder consume the same resolved value without Docker access. Cover regular
   normalization to one `rw` bind, linked normalization to three safely ordered binds, each missing required role,
   and refusal to drop mounts when that would weaken `ro` isolation. Keep public APIs wired until Phase 3.

### Phase 3: Atomic Public Cutover
Purpose: Switch init and both launchers to the typed resolver and role-aware Docker plan in one green change.
Status: to be done
Done when: public init and launch paths use typed config and legacy flat/root-ignore code is gone; exact mount-based
reuse remains intentionally incomplete until Phase 4 and this phase is not release-ready on its own.

1. Refactor `internal/cli.Config`, `Dependencies`, and `Run` to keep flag parsing and `agents-safe`'s missing-command
   usage check before discovery, then resolve defaults -> TOML -> explicit flags -> validation. Defer only the
   config-dependent Codex argv merge until after config loading.
2. Track CLI values separately from intent. Apply `--image`, `--no-host-mcp`, and `--no-host-mcp=false` only when the
   matching flag changed; only explicit `--image` sets `ImageOverride`, including when its value equals the config.
3. Change `projectenv.Initialize` and `cmd/agents-safe.runInit` to serialize the typed defaults, retain the embedded
   Dockerfile sample, create the exact `.agents-safe/.gitignore`, preserve existing regular files, reject symlinks,
   and never contact Docker or modify the worktree-root `.gitignore`. Call the Phase 2 host/default helpers directly;
   preserve the focused test proving init never constructs a `DockerLauncher`.
4. Change `DockerLauncher.Launch`, `launchAttempt`, and `docker_requests.go` to consume the resolved role-aware plan.
   Derive legacy diagnostic labels and command `CODEX_HOME` from present roles; without `codex_home`, use ephemeral
   Codex state and emit the exact degradation warning before Docker access.
5. Materialize `host_mcp_channel` only when the role is present, forwarding is enabled, and endpoints are eligible.
   Print missing degradable-role warnings once in stable role order and never reinsert a deleted role. Both binaries
   warn when config removes a default `codex_home`; when no host default exists, warn only for `codex-safe`.
6. Delete `LoadMounts`, the embedded static config resource, old configured-mount assembly, `updateGitignore`, and the
   Codex-home prompt, creation policy, and `missingCodexHome` special path only after production callers use typed
   config. Retain existing Codex-home/skills/MCP label comparisons as a temporary Phase 3 reuse guard.
7. Update `internal/cli`, both `cmd/*` suites, `internal/testutil/clitest`, launcher request/user-mount/MCP tests, and
   init tests for usage-error ordering, precedence, full-file validation, exact warnings, no Docker-before-warning,
   optional omission, required omission, stale present paths, and idempotent local files.

### Phase 4: Creation-Time Fingerprint and Active Reuse
Purpose: Reject any running session whose immutable creation parameters differ from the current request.
Status: to be done
Done when: only an exact fingerprint match can reach `docker exec`, while mismatches leave the active container
untouched.

1. Add `internal/launcher/launch_config.go` with a private versioned canonical fingerprint input and
   `codex-safe.launch-config` label. Set `schema_version` to `1`, encode ordered structs/slices deterministically, and
   hash them with SHA-256; do not hash maps or TOML bytes.
2. Implement exactly the design-defined fields: requested image reference, explicit-image intent, ordered normalized
   physical filesystem mounts as source/target/read-only triples, `no_host_mcp`, and canonical effective MCP
   `host:port` addresses sorted by host and port. Exclude the random MCP channel bind, redundant logical aliases,
   comments, endpoint server names, argv, built image IDs, Dockerfile contents, and separate ownership/protocol data.
3. Resolve MCP eligibility and compute the requested fingerprint after config/mount validation but before
   `acquireContainer` and `prepareImage`. Distinguish disabled MCP from enabled MCP with no eligible endpoints even
   though neither materializes a channel mount.
4. Attach the fingerprint label in `buildCreateRequest` and compare it after ownership/protocol checks on every path
   that can adopt a running container: initial reuse, stopped-name wait, concurrent-create winner, and post-exec
   replacement retry. A missing pre-feature label is incompatible.
5. Return one mismatch error containing worktree, running fingerprint, requested fingerprint, and the instruction to
   finish the active session. Do not build an image, stop/replace the container, create a sidecar, or execute a command
   after mismatch; stopped containers keep the current wait-for-release behavior unless they become reusable.
6. After fingerprint comparison covers every adoption path, remove the temporary Codex-home/skills/MCP label
   comparisons. Keep those labels only for diagnostics and MCP channel adoption; they must not form an alternate
   compatibility path. Add focused tests proving absent-to-present `codex_home` uses the generic mismatch, every
   creation field matters, and every command-time or descriptive field does not.

### Phase 5: Public Contract, Real Boundary, and Handoff
Purpose: Align public guidance and prove that serialized settings match the real Docker container.
Status: to be done
Done when: public init/config behavior is documented, real inspect data matches it, and the implementation is ready
for owner review.

1. Update `cmd/agents-safe` init wiring and help so it passes the discovered project and typed defaults into
   initialization, names all three local files, and promises no root `.gitignore` mutation or Docker contact.
2. Replace the flat config/root-ignore examples in `README.md` and relevant package READMEs. Verify the implemented
   fingerprint fields and sole compatibility path against `codex-safe.md` section 10 and
   `host-mcp-forwarding.md` section 5. Keep durable contracts in the owning design docs, and change the
   launcher-config design status from `Proposed` to `Implemented` only after shipped behavior and tests match them.
3. Replace the flat-config smoke fixture in `tests/smoke/sysbox_agents_test.go`. Extend the linked-worktree scenario to
   run public init, edit/use the typed config, inspect the session container, and compare source, destination, and
   read/write mode for every normalized physical mount; trace each inspected bind back to its logical role or roles.
   Remove one degradable role and assert its exact warning and physical absence.
4. In a regular checkout, assert init writes all three required logical roles, `docker inspect` reports their single
   normalized `worktree:rw` bind, and `git status`, `git add`, and a ref read work. In both regular and linked
   topologies, prove removal of each required role fails before Docker create. Then add a real running-container
   mismatch scenario for a degradable creation-time edit. Assert the invocation fails while the old container is
   active, its ID is unchanged, and no replacement command starts; also prove a command-time Codex argument edit
   takes effect through reuse.
5. Run the complete validation sequence below. Record a missing `sysbox-runc` or other pre-execution host limitation
   as an environment blocker rather than feature success; do not claim real mount parity without the smoke evidence.
6. Add dated progress notes, move this plan to `docs/exec-plans/review/`, update the plan index, and request owner
   acceptance only after all non-environment-blocked gates and implementation review findings are resolved.

## Validation Gates

- `gofmt` all changed Go files before Go validation.
- `go test ./internal/launcher/projectenv` and `go test ./...` pass after Phase 1.
- `go test ./internal/launcher/projectenv ./internal/launcher/launchplan` passes after Phase 2.
- `go test ./internal/launcher` and `go test ./...` pass after Phase 2 host-environment extraction.
- `go test ./internal/cli ./cmd/agents-safe ./cmd/codex-safe ./internal/launcher` and `go test ./...` pass after the
  atomic Phase 3 cutover.
- `go test ./internal/launcher` and `go test ./...` pass after Phase 4 fingerprint cutover.
- `go vet ./internal/launcher/projectenv ./internal/launcher/launchplan ./internal/cli ./internal/launcher` passes.
- `make lint` passes.
- `make test` passes, including compile-only smoke packages.
- `make check-docs` passes.
- `make test-smoke-go` passes on a Linux host with `sysbox-runc`; inspect assertions match the normalized physical
  plan, preserve logical-role traceability, and prove the active-container mismatch leaves the original session
  untouched.
- `git diff --check` passes.
- `rg -n 'LoadMounts|updateGitignore|configSampleContent' cmd internal tests` returns no legacy implementation symbol.
- `rg -n 'missingCodexHome|confirmCreateCodexHome|CodexHomePolicy' cmd internal tests` returns no prompt-policy symbol.
- `rg -n 'validateResolvedRunningUserMounts|userMountMismatchError' cmd internal tests` returns no alternate reuse path.
- `rg -n 'updates? the root \.gitignore|add exact rules.*root' README.md cmd internal docs/design-docs tests` returns no
  stale root-ignore user contract.

## Risks and Constraints

- The config is a host-specific snapshot. A moved/deleted source must fail closed; never refresh it silently after
  initialization.
- A missing `codex_home` is degradable for both launchers. `codex-safe` uses ephemeral container-local state and warns;
  no launcher creates the host directory or silently re-adds the deleted role.
- Missing degradable roles still change creation-time state. A cold launch may proceed after warning, but a running
  container created with a different mount set remains incompatible and is not replaced.
- Mount normalization must collapse the three regular-checkout roles to one writable bind without treating the
  linked-worktree primary checkout as redundant. Preserve the broad read-only primary mount before its narrower
  writable common-Git override.
- `no_host_mcp = true` and enabled forwarding with no eligible endpoints both create no channel, but remain distinct
  creation-time policies in the fingerprint.
- Project image preparation stays after active-session compatibility. Do not make a mutable tag, rebuilt image ID, or
  Dockerfile contents invalidate a running container.
- Keep the random MCP generation directory out of compatibility; it is session plumbing, not requested state.
- Preserve unrelated user changes and the already-approved dependency set.

## Out of Scope

- Migration or backward-compatible parsing of the flat `mounts = []` schema.
- Configuring `--project`, positional agent commands, one-invocation Codex arguments, container naming, resource
  limits, timeouts, relay internals, or nested-Docker state.
- Automatically updating existing project config after host paths change.
- Stopping or replacing an incompatible active container.
- Adding dependencies or changing the base/session image contents.

## Progress Notes

- 2026-07-19: Plan created from the approved launcher-configuration design and live implementation baseline.
- 2026-07-19: Stabilized the config schema around three always-required logical project/Git roles. Regular checkouts
  normalize those roles to one physical bind; linked worktrees retain three physical binds.
