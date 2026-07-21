# Exec Plan: uv Host-Backed Dependency Cache

- Status: active
- Created: 2026-07-21
- Target executor: GPT-5.6 Terra
- Design:
  - [`docs/design-docs/host-backed-dependency-caches.md`](../../design-docs/host-backed-dependency-caches.md)
  - [`docs/design-docs/project-launcher-configuration.md`](../../design-docs/project-launcher-configuration.md)
- Scope:
  - `uv` / `UV_CACHE_DIR` only; preserve the implemented Go profiles
  - `agents-safe init`, host discovery, typed cache planning, fingerprinting, labels, and Docker exec routing
  - focused Go tests, real Sysbox uv coverage, and owning documentation

## Objective

Add live reuse of one existing host uv cache to both managed launchers without mounting the host home or creating an
`agents-safe` cache copy. Resolve the project-effective cache path once during `agents-safe init`, persist `kind =
"uv"` plus its absolute source, mount the physical directory read-write at the same container path, and route every
managed command through `UV_CACHE_DIR`.

Keep uv concurrency, bucket versioning, cache cleanup, refresh behavior, and link-mode fallback tool-owned. This plan
must not install uv in the base image or broaden support to Maven, Gradle, nested containers, or image-build caches.

## Done Criteria

- `uv` is the only new dependency-cache kind; the canonical order is `go_build`, `go_modules`, then `uv`.
- `agents-safe init --host-caches` accepts `uv` in `auto` and explicit duplicate-free subsets.
- uv discovery runs one bounded, no-shell `uv cache dir --directory <project-root>` probe for a new config only.
- Only an absent `uv` executable permits fallback to `UV_CACHE_DIR`, `$XDG_CACHE_HOME/uv`, then
  `<host-home>/.cache/uv`.
- Auto discovery omits an unavailable or unsafe uv path with a diagnostic; explicit uv selection fails atomically.
- Empty, multi-line, relative, temporary, missing, inaccessible, and forbidden-overlap uv paths are never persisted.
- Existing `.agents-safe/config.toml` files are neither rediscovered nor rewritten.
- A configured uv cache is a same-path writable bind and every managed command receives its target as `UV_CACHE_DIR`.
- The launcher does not set `UV_LINK_MODE`, `UV_NO_CACHE`, `UV_LOCK_TIMEOUT`, or uv Python/tool/config/auth paths.
- Existing Go cache ordering, routing, label values, and creation fingerprints remain unchanged; only the new uv
  diagnostic label is added.
- Fingerprint schema stays at version 2; adding, removing, or changing uv config rejects incompatible live reuse.
- Concurrent host/container and two-worktree uv operations complete without launcher locks or direct cache edits.
- Real Sysbox coverage proves native-to-container and container-to-native reuse, cold-session persistence, ownership,
  and no implicit uv cache access in project-image builds or nested containers.
- The base runtime image remains uv- and Python-free; uv behavior is exercised through a test-only project image.
- Focused, repository-wide, documentation, and compatible-host smoke gates pass.

## Current Baseline

- `projectenv.DependencyCacheKind` and `DependencyCacheKindOrder` contain only `go_build` and `go_modules`;
  `supportedDependencyCacheKind` rejects `uv`.
- `launchcli.ParseHostCacheSelection` delegates kind acceptance to `dependencies.IsGoCacheKind`, so `auto` and
  explicit parsing are Go-only.
- `launchcli.ResolveHostCaches` calls only `ResolveGoCaches` and receives no project root or complete default config
  for pre-persistence overlap validation.
- `internal/launchcli/dependencies/go_deps.go` provides the existing bounded-probe, absent-binary fallback, access,
  and auto-versus-explicit behavior to mirror for uv.
- `launchplan.DependencyCache.EnvironmentKey` returns `GOCACHE` for `go_build` and defaults every other accepted value
  to `GOMODCACHE`; it must become an explicit three-kind mapping before `uv` is accepted.
- Generic cache mount planning already preserves configured target versus physical source, validates effective access
  and overlaps, and orders entries through `DependencyCacheKindOrder`.
- Fingerprint schema version 2 already includes ordered cache kind, physical source, environment key, and normalized
  mounts. A new kind needs coverage, not a schema change.
- `DockerLauncher.buildExecRequest` already emits each planned cache's environment key per exec, while the create
  request attaches Go-specific cache labels; neither path emits a `codex-safe.uv-cache` label yet.
- `agents-safe init` usage, output, and tests name Go only. Its lazy config provider already avoids discovery for an
  existing config.
- `container/Dockerfile` intentionally contains neither uv nor Python. `tests/smoke/sysbox_go_caches_test.go`
  demonstrates how to keep dependency toolchains inside test-only project images.

## Implementation Decisions

- Kind identity: add `projectenv.DependencyCacheUV = "uv"` and append it after both Go kinds. Do not reorder existing
  kinds or add a user-configurable policy.
- Atomic exposure: resolver code may land while `uv` is still rejected, but the parser must accept `uv` only in the
  phase that also supplies mount routing, labels, fingerprint coverage, and exec environment behavior.
- Host probe: invoke `uv cache dir --directory <project-root>` with `exec.CommandContext`, no shell, and the same
  five-second timeout boundary used for Go unless measurement proves that insufficient.
- Probe form: `--directory` is a uv global option, so confirm the exact argv (flag accepted after the `cache dir`
  subcommand, else place it before) against the pinned uv version before building the resolver on it.
- Probe interpretation: trim one trailing line ending, then require exactly one non-empty canonical absolute path.
  Do not parse uv TOML, scrape verbose output, or accept a disappeared `--no-cache` temporary directory.
- Fallback boundary: use environment/Linux defaults only for `exec.ErrNotFound`. Timeout, cancellation, nonzero exit,
  malformed output, or an installed uv that reports an invalid path must remain visible.
- Existing directories: discovery never creates, warms, cleans, chmods, or chowns a cache. Auto omits an unavailable
  kind; explicit selection fails before writing config.
- Init safety: validate the base launch plan once, then validate each canonical candidate snapshot through
  `launchplan.ResolveWithHostHome` before encoding. This reuses the launcher's full physical overlap policy.
- Routing: inject only `UV_CACHE_DIR=<configured-target>` through `DockerLauncher.buildExecRequest`. Do not change the
  session protocol, `internal/container`, command argv, or unrelated uv variables.
- Link behavior: do not set `UV_LINK_MODE`. uv owns clone, hardlink, and copy fallback based on the actual cache and
  environment filesystems.
- Compatibility: keep fingerprint schema version 2. An uv entry naturally changes both `mounts` and
  `dependency_caches`; an empty or Go-only plan must hash exactly as before.
- Diagnostics: add `codex-safe.uv-cache` with the physical source or `absent`, matching existing non-authoritative Go
  labels. Fingerprint and normalized mounts remain the reuse authority.
- Tool availability: configuring uv does not prove the project image contains `uv`; command-not-found remains the
  requested command's normal error.
- Test toolchain: obtain explicit owner approval before adding any new external uv/Python image or downloaded artifact.
  Pin every approved smoke-only image or binary by digest/checksum and keep it out of `container/Dockerfile`.
- Dependencies: add no Go module dependency. Any implementation-time dependency proposal pauses for owner approval.

## Phases

### Phase 1: Docker-Free uv Resolver
Purpose: Resolve and validate an existing host uv cache without making `uv` a launchable config kind.
Status: to be done
Done when: resolver tests cover the complete probe and fallback contract while production config still rejects uv.

1. Add `DependencyCacheUV` in `internal/launcher/projectenv/config.go`, but leave the supported-kind predicate and
   canonical order Go-only until Phase 2.
2. Add `internal/launchcli/dependencies/uv_deps.go` with injected command, environment, stat, access, project-root,
   host-home, and timeout inputs. Reuse package access constants instead of duplicating them.
3. Confirm the exact `uv cache dir --directory <project-root>` argv against the pinned uv version, then run exactly
   that under a bounded child context and capture stdout without a shell. Treat context expiry and parent cancellation
   distinctly enough for useful errors.
4. Accept one trimmed absolute path. Reject empty or multi-line output, unsafe lexical forms, a missing/non-directory
   path, and failed read/write/search access.
5. On `exec.ErrNotFound` only, resolve `UV_CACHE_DIR`, then `$XDG_CACHE_HOME/uv`, then
   `<host-home>/.cache/uv`; require absolute environment bases and the same directory/access validation.
6. Return one typed candidate or the same auto diagnostic versus explicit error shape used by the Go resolver. Do not
   create a directory and do not hide the underlying probe cause.
7. Add `uv_deps_test.go` cases for success, CRLF trimming, multi-line output, timeout, cancellation, nonzero exit,
   missing executable, fallback precedence, relative env values, temporary/disappeared paths, access failure, auto
   omission, explicit failure, and zero probes when uv is unselected.
8. Run `gofmt`, focused dependency tests, `go vet ./internal/launchcli/dependencies`, and `git diff --check`.

### Phase 2: Atomic uv Launch Contract
Purpose: Make a manually configured uv cache safe and effective before init can generate one.
Status: to be done
Done when: accepted `kind = "uv"` config produces the validated bind, diagnostics, fingerprint, and per-command routing.

1. Append `DependencyCacheUV` to `DependencyCacheKindOrder` and accept it in
   `projectenv.supportedDependencyCacheKind`; update decode, encode, duplicate, deep-clone, and unknown-kind tests.
2. Change `launchplan.DependencyCache.EnvironmentKey` to return `(string, error)`. Enumerate `GOCACHE`, `GOMODCACHE`,
   and `UV_CACHE_DIR` explicitly, return an error for every other kind, and propagate that error through creation
   fingerprinting and exec-request construction.
3. Reuse `resolveDependencyCaches` and `validateDependencyCache` for uv; add tests proving canonical order, physical
   symlink source, same configured target, writable bind, full overlap rejection, and coexistence with both Go kinds.
4. Add `codex-safe.uv-cache` beside the two Go labels in `container_lifecycle.go` and `docker_requests.go`; emit the
   physical source or `absent` deterministically.
5. Extend exec-request tests to prove `UV_CACHE_DIR` follows the Go variables in canonical order, overrides an
   image-owned value, and is absent when unconfigured.
6. Before changing production fingerprint inputs, add fixed expected digests for representative empty and Go-only
   version-2 fixtures and prove they pass. Then extend the tests to prove one uv entry changes identity and mutable
   cache contents do not participate while both baseline digests remain unchanged.
7. Add a focused inherited-environment regression only where needed; do not modify `internal/session`,
   `internal/container`, or `cmd/codex-safe-session` production code.
8. Run `gofmt`, focused projectenv/launchplan/launcher tests, `go vet` for those packages, `make test`, and
   `git diff --check`.

### Phase 3: Noninteractive uv Initialization
Purpose: Add uv to new-project cache discovery without changing existing-config idempotence.
Status: to be done
Done when: auto and explicit init persist only safe canonical snapshots and never probe an existing config.

1. Generalize `ParseHostCacheSelection` to accept exactly `go_build`, `go_modules`, and `uv`; keep sentinels,
   duplicate rejection, empty-token rejection, and canonical output order unchanged.
2. Change `ResolveHostCaches` to orchestrate the existing Go resolver and the new uv resolver without repeating a
   family probe. Merge candidates and diagnostics in `DependencyCacheKindOrder`.
3. Pass the selected `gitproject.Project`, complete generated defaults, and canonical host home into orchestration so
   uv probes from the selected worktree and candidate snapshots can use full launch-plan validation.
4. Validate the no-cache base plan before discovery. Then add candidates one at a time in canonical order and call
   `launchplan.ResolveWithHostHome`; auto omits the candidate named by a validation failure with a diagnostic, while
   explicit selection returns an error and writes nothing.
5. Preserve a non-nil empty slice for `none`, zero tool probes for `none`, lazy zero-probe behavior for an existing
   config, and one Go probe even when both Go kinds are selected.
6. Update `agents-safe init` usage, flag help, and success output to include uv while preserving exact Go output and
   the existing empty-auto and unchanged-snapshot diagnostics.
7. Add command and launchcli tests for arbitrary explicit input order, canonical persisted order, mixed-family
   success, one-family failure, project-local uv `cache-dir`, forbidden overlap, existing config, and no Docker/network
   calls.
8. Run `gofmt`, focused command/launchcli/projectenv tests, `go vet` for those packages, `make test`, and
   `git diff --check`.

### Phase 4: Real uv and Sysbox Boundary Proof
Purpose: Prove bidirectional uv cache reuse with real tools and mounts rather than request-shape tests alone.
Status: to be done
Done when: native host uv and uv in test-only project images reuse one cache in both directions across cold sessions.

1. Before adding a smoke-only uv/Python source, present the exact image/artifact version and digest/checksum for owner
   approval. Do not change `container/Dockerfile` or add an unpinned download.
2. Add `tests/smoke/sysbox_uv_cache_test.go` and minimal helpers for a host uv prerequisite, one temporary cache, two
   independent project environments, and an approved digest-pinned uv/Python project image.
3. Generate two minimal pure-Python wheels and their PEP 503 index pages with Go standard-library fixture code. Reserve
   one loopback port and use the exact URL `http://127.0.0.1:<port>/simple` in both environments. Serve the first index
   on host loopback, seed the shared cache with native uv, stop the server, and remove every install target before
   offline consumption.
4. Use the exact seeded index URL and requirement through the public launcher with `--offline`. First prove an empty
   control cache fails, then prove the configured shared cache installs into a fresh container environment after the
   server is gone.
5. Serve the second index on container loopback at the same exact URL, seed it with container uv, stop the server, end
   the session, and repeat the empty-cache failure plus shared-cache success from native host uv and a new cold
   session. Keep wheel files unreachable through the URL while consuming; never edit cache files directly.
6. Give the test image a conflicting `UV_CACHE_DIR`; prove the managed value wins, the bind is writable, same-path,
   and `rprivate`, and created files retain host UID/GID.
7. Run the focused `TestSysboxUVHostCacheReuse` test. Record the exact pre-execution prerequisite if Docker, Sysbox,
   host uv/Python, or the approved pinned image is unavailable.

### Phase 5: uv Concurrency, Reuse, and Isolation
Purpose: Prove the shared cache remains safe across overlapping clients and creation-time boundaries.
Status: to be done
Done when: concurrent worktrees succeed and uv state never leaks into an incompatible or nested container.

1. Add `TestSysboxConcurrentUVCacheWorktrees`: run two worktrees concurrently with separate environments against the
   same cache. Do not share `.venv` and do not add a launcher lock.
2. Add `TestSysboxUVCacheConfigMismatch`: change uv config during a live session and prove reuse is rejected without
   stopping, replacing, or mutating that session.
3. Prove project-image build steps and a nested Docker container receive neither the cache bind nor `UV_CACHE_DIR`
   implicitly.
4. Run every focused `TestSysboxUV...` test and the complete `make test-smoke-go` gate on Linux/Sysbox. Record the
   exact pre-execution prerequisite if the real-host gate is unavailable.

### Phase 6: Documentation and Review Handoff
Purpose: Align public guidance and durable contracts with the shipped uv slice and hand off verified work for review.
Status: to be done
Done when: docs describe Go plus uv as implemented, Maven/Gradle as deferred, and all review findings are resolved.

1. Update `README.md`, `internal/launcher/README.md`, `internal/launcher/projectenv/README.md`,
   `internal/launcher/launchplan/README.md`, and `tests/smoke/README.md` for uv init, config, routing, diagnostics, and
   smoke behavior.
2. Update both owning design docs in the same change: mark uv implemented, keep fingerprint schema version 2, and keep
   Maven and Gradle deferred. Preserve the rule that per-exec routing belongs to `docker_requests.go`.
3. Document that uv and Python remain project-image responsibilities, `UV_LINK_MODE` is not launcher-managed, and
   cleanup must use uv only when no conflicting host/container work is active.
4. Run an implementation review against the design, this plan, the live diff, and real smoke evidence. Record fixes
   and verification in the same feature-review artifact.
5. Run all focused tests, `make lint`, `make test`, `make check-docs`, `make test-smoke-go`, and `git diff --check`.
6. Add dated progress outcomes, set every completed phase to `done`, move this plan to `review/`, and update the plan
   index only after every non-blocked gate passes.

## Validation Gates

- `gofmt` runs on every changed Go file.
- `go test ./internal/launchcli/dependencies` passes after Phase 1.
- `go vet ./internal/launchcli/dependencies` passes after Phase 1.
- `go test ./internal/launcher/projectenv ./internal/launcher/launchplan ./internal/launcher` passes after Phase 2.
- `go vet ./internal/launcher/projectenv ./internal/launcher/launchplan ./internal/launcher` passes after Phase 2.
- `go test ./cmd/agents-safe ./internal/launchcli/... ./internal/launcher/projectenv` passes after Phase 3.
- `make lint` and `make test` pass before review handoff.
- `make check-docs` passes after each documentation change.
- The focused `TestSysboxUV...` tests and `make test-smoke-go` pass on a compatible Linux/Sysbox host; an unavailable
  prerequisite is reported as an environment blocker, not replaced by unit-test evidence.
- `git diff --check` passes after every phase.
- `rg -n 'UV_LINK_MODE|UV_NO_CACHE|UV_LOCK_TIMEOUT|UV_PYTHON_INSTALL_DIR|UV_TOOL_DIR' internal/launcher cmd \
  --glob '*.go' --glob '!*_test.go'` returns no production matches.
- `rg -n 'uv|UV_CACHE_DIR' internal/container internal/session cmd/codex-safe-session container` returns no production
  routing or base-image installation.
- A focused fingerprint fixture proves empty and Go-only version-2 digests remain byte-for-byte unchanged.
- Inspecting the live smoke container proves exactly one uv cache bind and label, with no whole-home or parent-cache
  mount.

## Risks and Constraints

- `uv cache dir` is configuration-aware. A project can select a relative cache under its own worktree; init must reject
  that overlap before persisting it.
- `--no-cache`, `UV_NO_CACHE`, and `no-cache = true` select a temporary directory that disappears after the probe.
  Missing-after-probe is an unavailable cache, never a fallback trigger while uv is installed.
- Multiple uv releases safely share versioned buckets but may duplicate data. Do not add launcher version checks or
  cleanup.
- The cache can contain platform-built wheels and cached environments. Same Linux kernel/architecture does not remove
  libc, Python, or external-library compatibility risks; preserve tool errors and documented refresh controls.
- Cache and target environments on different filesystems lose fast linking. uv must own copy fallback; path strings or
  bind count are not reliable filesystem evidence.
- `uv cache clean` and `uv cache prune` coordinate with active uv processes. Tests and launcher cleanup must not edit
  live cache contents directly or invoke `--force`.
- A writable cache joins host and project code into one trust domain and may expose private packages or executable
  cached environments. Do not share it with untrusted projects.
- Real concurrency, ownership, mount propagation, and offline reuse require Sysbox evidence. Unit tests cannot prove
  those properties.
- The existing `make test-smoke-go` name covers all Sysbox scenarios; do not rename the gate in this feature.
- Do not add a dependency without explicit owner approval.

## Out of Scope

- Maven, Gradle, Docker image, BuildKit, registry, or artifact-proxy caching.
- Installing uv or Python in `container/Dockerfile` or changing the base session image.
- Sharing host virtual environments, uv-managed Python installations, installed tools, credentials, configuration,
  keyrings, or the entire host cache parent.
- Managing `UV_LINK_MODE`, `UV_NO_CACHE`, `UV_LOCK_TIMEOUT`, refresh flags, cache keys, cleanup, pruning, quotas, or
  telemetry.
- Automatic cache mounts or environment propagation into project-image builds or nested Docker containers.
- Launch-time rediscovery, existing-config migration/update commands, cache creation, warming, repair, or permission
  changes.
- Network filesystems, cross-host or multi-user sharing, macOS, Windows, remote Docker daemons, or cross-architecture
  emulation.
- Session protocol, manager lifecycle, command-argv rewriting, or Go cache behavior changes.

## Progress Notes

- 2026-07-21: Created from the accepted Go implementation baseline and the uv cache contract documented by Astral.
- Add dated implementation outcomes and review links before moving this plan out of `active/`.
