# Exec Plan: Go Host-Backed Dependency Caches

- Status: active
- Created: 2026-07-20
- Design:
  - [`docs/design-docs/host-backed-dependency-caches.md`](../../design-docs/host-backed-dependency-caches.md)
  - [`docs/design-docs/project-launcher-configuration.md`](../../design-docs/project-launcher-configuration.md)
- Scope:
  - `go_build` / `GOCACHE` and `go_modules` / `GOMODCACHE` only
  - `agents-safe init`, typed project configuration, launch planning, fingerprinting, and Docker exec routing
  - focused Go tests, real Sysbox smoke coverage, and owning documentation

## Objective

Implement live reuse of the host's existing Go build and module caches in both managed launchers. Resolve cache paths
once on the Linux host during `agents-safe init`, persist only `kind` and absolute `source`, mount each physical
directory read-write into the outer Sysbox container, and explicitly route every managed command through `GOCACHE`
and `GOMODCACHE`.

This is the first narrow delivery from the broader cache design. It must not expose configuration or behavior for uv,
Maven, Gradle, Docker images, BuildKit, or nested-container cache propagation.

## Done Criteria

- The implemented cache kinds are exactly `go_build` and `go_modules`; all other kinds and a serialized `mode` fail
  before Docker access.
- `agents-safe init --host-caches` accepts only `auto`, `none`, or a duplicate-free comma-separated subset of the two
  Go kinds.
- Default `auto` discovers every existing Go cache without creating directories, contacting Docker, or using network
  access.
- An existing `.agents-safe/config.toml` is neither rediscovered nor rewritten; init reports that its cache snapshot
  remains unchanged.
- Omitted and present-empty `common.dependency_caches` both resolve to no caches; a present non-empty list replaces the
  list as one value.
- Each configured path remains the tool-visible container target while its symlink-resolved physical directory is the
  bind source.
- Cache sources pass effective read, write, search, path-scope, and overlap validation before Docker access.
- Every configured Go cache is a same-path read-write bind in deterministic `go_build`, `go_modules` order.
- Every managed command, including commands entering a reused session, receives the configured `GOCACHE` and/or
  `GOMODCACHE`; unconfigured variables remain image-owned.
- Creation fingerprint schema version 2 covers the normalized binds and canonical Go cache routing contract, but not
  cache contents, timestamps, or size.
- A version-1 or differently configured active container is rejected without stop, replacement, build, or command
  execution.
- Real Sysbox coverage proves host/container reuse, ownership, concurrent Go access, cold-session persistence, and no
  implicit cache access from project-image builds or nested containers.
- Owning docs describe Go as implemented and uv, Maven, and Gradle as deferred; they do not claim full cache support.
- Required focused, repository-wide, documentation, and compatible-host smoke gates pass.

## Current Baseline

- `projectenv.CommonConfig` contains image, host-MCP policy, and logical mounts, but no dependency-cache field.
- `agents-safe init` accepts only `--project`, resolves the complete default config eagerly, and passes it to
  `projectenv.Initialize` even when `config.toml` already exists.
- `launchplan.Plan` contains normalized binds, mount-role provenance, and host-MCP presence, but no typed cache
  metadata or per-command routing.
- Generic mount validation follows symlinks for `os.Stat` but does not retain a distinct physical source and configured
  target.
- The creation fingerprint uses schema version 1 and contains no cache-specific canonical entries.
- `buildExecRequest` injects `HOME` and optional `CODEX_HOME`; it does not set `GOCACHE` or `GOMODCACHE`.
- `codex-safe-session run` leaves `CommandConfig.Environment` nil, so its child already inherits variables supplied by
  `docker exec --env`.
- The final base image does not contain the Go toolchain. The project Dockerfile sample already shows how to copy the
  pinned toolchain from a Go build stage.

## Implementation Decisions

- Go-only surface: define and accept only `go_build` and `go_modules`. Do not add dormant enum values, CLI choices,
  warnings, or code paths for other ecosystems.
- Canonical kind order: use `go_build` before `go_modules` in config snapshots, launch metadata, environment routing,
  fingerprints, labels, diagnostics, and tests.
- Host probe: run one bounded, no-shell `go env -json GOCACHE GOMODCACHE` command per new-config initialization,
  including when an explicit subset selects one kind.
- Fallback boundary: only an absent `go` executable permits host environment and Linux-default fallbacks. A timeout,
  nonzero exit, malformed JSON, `off`, or a relative value never silently falls back.
- Linux fallback: use explicit `GOCACHE` or `GOMODCACHE` first. Then use
  `$XDG_CACHE_HOME/go-build` or `<host-home>/.cache/go-build` for the build cache, and
  `<host-home>/go/pkg/mod` for modules.
- Existing directories only: `auto` omits an unavailable kind with a concise diagnostic; explicit selection fails.
  Initialization never creates, warms, cleans, changes ownership, or changes permissions on a cache.
- Existing config: discovery is lazy. Re-running init preserves the file, does not execute `go env`, and reports that
  `--host-caches` cannot update the existing snapshot; users edit the ignored config manually.
- Source identities: preserve the lexically normalized configured path as target and environment value. Resolve
  symlinks once to obtain the physical bind source and fingerprint identity.
- Access checks: use a Linux-specific effective-access helper backed by standard-library `syscall.Access`; add no
  dependency and do not perform a temporary write probe.
- Atomic manual-config support: do not merge the cache field until mount planning, fingerprint version 2, diagnostics,
  and exec routing are ready in the same phase. The tree must never accept a cache entry that it ignores.
- Per-exec routing: add Go variables in `DockerLauncher.buildExecRequest`. Keep `internal/container/`,
  `internal/session/`, `cmd/codex-safe-session/`, and the session protocol unchanged except for a focused inheritance
  regression test.
- Compatibility: bump schema version 1 to 2 for every launch, including an empty cache list. Existing active sessions
  therefore fail the normal creation-fingerprint comparison after upgrade.
- Diagnostic metadata: expose deterministic, non-authoritative cache metadata on the session container. Reuse remains
  governed only by the versioned fingerprint.
- Test toolchain: keep Go out of the base runtime image. Real tool behavior uses a test-only project image that copies
  the already pinned Go toolchain shown in `Dockerfile.sample`.

## Phases

### Phase 1: Go Milestone Contract and Host Resolver
Purpose: Narrow the shipped contract and build a testable Docker-free resolver without exposing new config yet.
Status: done
Done when: Go cache selections resolve deterministically from the host and no production launch accepts cache config.

1. Update the host-cache design's delivery matrix and resolver wording: only the two Go kinds are in this milestone,
   probe failure falls back only for an absent executable, and per-exec routing belongs to the host launcher.
2. Add `DependencyCacheKind` and `DependencyCacheConfig` types for `go_build` and `go_modules` without adding the field
   to `CommonConfig` yet.
3. Add `internal/launchcli/host_caches.go` with an injected command runner, environment lookup, host home, filesystem
   inspection, and bounded context.
4. Parse `auto`, `none`, and explicit subsets; reject empty tokens, duplicates, mixed sentinel/kind values, and every
   non-Go kind before discovery.
5. Resolve both values through one `go env -json` call, enforce the fallback boundary, validate each selected result
   independently, and return canonical-order entries plus diagnostics.
6. Add table-driven tests for selection grammar, one-probe behavior, timeout/error/malformed output, absent-binary
   fallbacks, missing directories, explicit failure, auto omission, and `none` with zero probes.
7. Run the focused launchcli/projectenv tests, `go vet` for those packages, `make check-docs`, and `git diff --check`.

### Phase 2: Atomic Manual Configuration and Runtime Contract
Purpose: Make a manually configured Go cache work end to end before init begins generating cache entries.
Status: done
Done when: accepted TOML produces safe binds, schema-v2 identity, diagnostics, and per-command Go routing.

1. Add `CommonConfig.DependencyCaches`, presence-aware `*[]DependencyCacheConfig` overlay, deep cloning, deterministic
   encoding, empty defaults, unique-kind validation, and strict unknown-key rejection in `projectenv`.
2. Extend launch resolution inputs with the canonical host home and add ordered resolved cache metadata to
   `launchplan.Plan` and cache-kind provenance to physical mounts.
3. For each cache, validate its configured target, resolve and validate its physical directory, and check effective
   read/write/search access with Linux and unsupported-platform helper files.
4. Reject root, host-home/ancestor, project, Git, Codex-home, skills, host-MCP, additional-mount, and cache-to-cache
   overlaps using both configured targets and symlink-resolved physical sources. Never let normalization absorb cache
   identity into a broader mount.
5. Append same-path writable binds in canonical kind order and include them in Docker create requests without changing
   the Docker transport shape.
6. Increment the creation fingerprint to schema version 2 and append ordered entries containing kind, physical source,
   and routing contract. Keep target in normalized `mounts` and exclude mutable cache contents.
7. Add deterministic diagnostic metadata and append `GOCACHE`, then `GOMODCACHE`, to every applicable exec request
   after the existing managed environment entries.
8. Add focused config, plan, request, fingerprint, active-reuse, and inherited-environment tests; run affected package
   tests, `go vet`, `make test`, and `git diff --check`.

### Phase 3: Noninteractive Go Cache Initialization
Purpose: Expose the working Go-only contract through idempotent `agents-safe init` UX.
Status: done
Done when: new configs receive the requested snapshot once and existing configs trigger no cache discovery.

1. Add `--host-caches=auto|none|go_build,go_modules` to init usage and parse it before Git discovery. Do not add a
   launch-time override.
2. Pass `context.Context` and the typed selection through the injected initialization dependency without constructing
   a Docker launcher.
3. Refactor `projectenv.Initialize` around a lazy config provider and return whether `config.toml` was created, while
   preserving the current non-symlink and create-if-missing rules for all local files.
4. For a missing config, resolve host defaults, invoke the Go resolver, attach its snapshot only to the config passed
   to the encoder, and serialize it once. `none` must produce an explicit empty list without probing Go.
5. Print exact persisted kind/path/`shared_rw` rows for a new config, the documented empty-auto diagnostic when no
   cache exists, and an unchanged-snapshot diagnostic for an existing config.
6. Add CLI and initialization tests proving invalid syntax precedes discovery, explicit unavailable kinds fail,
   existing config never calls the resolver, no file is rewritten, and no path contacts Docker.
7. Run focused command, launchcli, and projectenv tests, `go vet`, `make test`, and `git diff --check`.

### Phase 4: Real Go and Sysbox Boundary Proof
Purpose: Prove the contract with actual Go commands and host bind mounts rather than request-shape tests alone.
Status: in progress
Done when: a Go-capable project image reuses both host caches safely across real Sysbox sessions.

1. Add `tests/smoke/sysbox_go_caches_test.go` and minimal fixture helpers for init invocation, two cache directories,
   a local `file://` module proxy, and a project Dockerfile that copies the pinned Go toolchain.
2. Give the project image conflicting `GOCACHE` and `GOMODCACHE` defaults, then prove managed values win and both
   inspected binds are writable, same-path, and `rprivate`.
3. Seed dependencies with a native host Go command, consume them offline in the container, populate build/module state
   in the container, and consume it later from the host and a new cold session.
4. Verify all cache writes retain the invoking host UID/GID and run two worktrees concurrently against the same cache
   sources without adding launcher locks.
5. Change cache config while one session is active and prove fingerprint mismatch performs no reuse or replacement.
6. Prove the project-image build and a nested Docker container receive neither cache binds nor Go cache variables
   implicitly.
7. Run the focused smoke test and then `make test-smoke-go` on a Linux host with Sysbox. Record an exact pre-execution
   environment blocker if that runtime is unavailable.

### Phase 5: Documentation and Review Handoff
Purpose: Make shipped behavior, deferred ecosystems, validation evidence, and remaining work unambiguous.
Status: in progress
Done when: docs describe the implemented Go slice accurately and the validated plan is ready for review.

1. Update `README.md`, package READMEs, and `tests/smoke/README.md` with Go-only init, config, routing, and test
   behavior.
2. Update both owning design docs to make schema version 2 and Go support implemented while keeping uv, Maven, and
   Gradle explicitly deferred; do not mark the full broad design implemented.
3. Correct the code-ownership map so per-exec routing points to `internal/launcher/docker_requests.go`, not a new
   container/session implementation.
4. Record implementation responses in the applicable feature-review artifact and run an implementation re-review.
5. Run `gofmt`, all focused tests, `make lint`, `make test`, `make check-docs`, the available Sysbox gate, and
   `git diff --check`.
6. Add dated outcomes, move this plan to `review/`, and update the execution-plan index only after all non-blocked
   gates pass.

## Validation Gates

- `gofmt` runs on every changed Go file.
- `go test ./internal/launchcli ./internal/launcher/projectenv` passes after Phase 1.
- `go vet ./internal/launchcli ./internal/launcher/projectenv` passes after Phase 1.
- `go test ./internal/launcher/projectenv ./internal/launcher/launchplan ./internal/launcher ./internal/session`
  passes after Phase 2.
- `go vet ./internal/launcher/projectenv ./internal/launcher/launchplan ./internal/launcher ./internal/session`
  passes after Phase 2.
- `go test ./cmd/agents-safe ./internal/launchcli ./internal/launcher/projectenv` passes after Phase 3.
- `make lint` and `make test` pass before review handoff.
- `make check-docs` passes after every documentation change.
- `make test-smoke-go` passes on a compatible Linux/Sysbox host; otherwise the exact missing runtime or Docker
  prerequisite is recorded as an environment blocker.
- `git diff --check` passes after every phase.
- `rg -n 'uv|maven|gradle|MAVEN_OPTS|GRADLE_USER_HOME|UV_CACHE_DIR' cmd internal tests --glob='*.go'` finds no
  implemented dependency-cache profile outside test assertions that unsupported kinds are rejected.
- `rg -n 'GOCACHE|GOMODCACHE' internal/container cmd/codex-safe-session container` finds no production routing logic.

## Risks and Constraints

- Schema version 2 invalidates all active version-1 sessions, even when the resolved cache list is empty.
- `go env` can reflect `GOENV` and host environment state. Persist its result once; ordinary launches must never
  rediscover or silently change it.
- ACL-aware effective access cannot be inferred from mode bits. Keep the Linux access check isolated and injectable.
- A symlinked configured target and its physical source are two deliberate identities; conflating them breaks either
  tool routing or overlap/fingerprint safety.
- The Go build cache does not detect changes to C libraries consumed through cgo. Preserve the documented
  `go clean -cache` operator recovery path.
- The module cache can contain private source and read-only files. Never recursively chmod, chown, clean, or copy it.
- Host and container writers share one trust domain. Go supports concurrent cache use, but project code can still
  delete or poison host-visible entries.
- The final base image has no Go binary. Smoke coverage must use a project image and must not expand this plan into a
  base-image toolchain change.
- Real ownership, concurrency, and mount behavior require Sysbox evidence; unit tests cannot substitute for that gate.
- Do not add a dependency. Any dependency proposal requires separate owner approval.

## Out of Scope

- uv cache discovery, `UV_CACHE_DIR`, and uv cache mounts.
- Maven local repository discovery, `MAVEN_OPTS`, Resolver coordination, or Maven mounts.
- Gradle cache discovery, `GRADLE_USER_HOME`, Gradle locks, or Gradle mounts.
- Docker image-layer persistence, registry mirrors, BuildKit cache mounts, volumes, or private-daemon persistence.
- Automatic environment or bind propagation into nested Docker containers.
- Go installation in the base session image or changes to `container/Dockerfile`.
- Launch-time cache rediscovery, config migration/update commands, cache warming, cleanup, pruning, quotas, or
  telemetry.
- Read-only seeds, snapshots, remote cache services, network filesystems, cross-host sharing, macOS, Windows, remote
  Docker daemons, or cross-architecture emulation.
- Changes to `codex-safe-session` protocol, manager lifecycle, or command argv rewriting.

## Progress Notes

- 2026-07-20: Created the Go-only implementation plan from the accepted broad design and current launcher code.
- 2026-07-20: Read-only planning confirmed that per-exec Docker environment injection reaches the managed child, so
  no container/session runtime or base-image change is required.
- 2026-07-20: Implemented Go-only config, host resolution, safe cache bind planning, schema-v2 fingerprints,
  diagnostics, and `docker exec` routing. Focused tests, `make test`, lint, and documentation validation pass.
- 2026-07-20: Implementation review fixed the test-only Go image to use the repository's pinned Go digest; see
  `docs/reviews/feature-review/2026-07-20-go-host-backed-dependency-caches-implementation-review.md`.
- 2026-07-20: Completed real Sysbox coverage for the Go cache contract: same-path writable mounts and routing,
  host-native seeding from a local `file://` module proxy, offline reuse in a live session and a new cold session,
  host-visible writes/ownership, nested-Docker environment isolation, and live-session fingerprint mismatch rejection.
  `TestSysboxGoHostCaches` and `TestSysboxGoCacheConfigMismatch` pass on this Linux/Sysbox host.
- 2026-07-20: `make test-smoke-go` rebuilt the base image and passed its first Go-independent smoke test, then the
  pre-existing `TestSysboxRegularCheckoutNormalizesProjectRoles` failed before its ready barrier with Sysbox exec
  `exit 137`; container inspection reported `OOMKilled=false`, `ExitCode=1`, and immediate removal. This host-runtime
  failure prevents completion of the aggregate smoke gate, but does not invalidate the focused Go cache evidence.
