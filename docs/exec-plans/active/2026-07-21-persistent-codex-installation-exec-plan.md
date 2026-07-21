# Exec Plan: Persistent Container Codex Installation

- Status: active
- Created: 2026-07-21
- Design: `docs/design-docs/persistent-codex-installation.md`
- Scope:
  - `cmd/codex-safe/`, `cmd/codex-safe-session/`, and `internal/codexinstall/`
  - `internal/launcher/`, including `internal/launcher/dockercli/`
  - `internal/container/` and `container/`
  - `tests/smoke/`, `Makefile`, and user, architecture, dependency, operation, and design docs

## Objective

Decouple routine Linux Codex updates from base- and project-image rebuilds. Store validated releases in a
Docker-managed volume, expose that volume read-only to normal sessions, and make `codex-safe update` the only
supported writer. Keep the update path usable with a macOS Docker daemon even though the Sysbox session backend
remains Linux-only.

## Done Criteria

- `codex-safe update` installs a validated Linux Codex release without Git discovery or a project/image rebuild.
- Every `codex-safe` and `agents-safe` session uses the same owned store for one Docker daemon, UID, protocol, and
  Linux target, mounted read-only below the effective container `CODEX_HOME`.
- Host-native standalone packages are hidden by the narrower volume and are never selected by the dispatcher.
- Empty stores use the verified image bootstrap, while malformed initialized stores fail with a recovery diagnostic.
- Concurrent readers remain usable during an update, and only one trusted maintenance container can publish.
- Failure or cancellation before the atomic commit leaves the previous `current` release selected.
- Routine release changes do not alter session identity; store protocol, target, identity, and mount mode do.
- Focused, repository-wide, image, ordinary-Docker, Linux/Sysbox, documentation, and macOS Docker Desktop gates pass
  or an exact unavailable-host prerequisite is recorded.

## Current Baseline

- `container/Dockerfile` downloads and verifies Codex `0.144.4`, then installs the single release binary directly at
  `/usr/local/bin/codex`; every Codex update therefore requires an image rebuild.
- `cmd/codex-safe/main.go` delegates every invocation to the project launcher. It has no host-side subcommand and
  always reaches Git/config discovery.
- `cmd/codex-safe-session` implements `serve`, `run`, `wait-ready`, and `relay`; it has no Codex dispatcher or
  maintenance entrypoint.
- `internal/launcher/dockercli` can inspect images and containers, but has no daemon-target or volume API. Its mount
  transport always emits `type=bind` with `rprivate` propagation.
- Session create requests append only bind mounts from `launchplan` and host-MCP state. Creation fingerprint schema 2
  has no named-volume or Codex-store input.
- Project-image architecture validation compares image architecture with host `runtime.GOARCH`, and launcher session
  preflight rejects non-Linux hosts.
- Existing Sysbox tests prove that `/usr/local/bin/codex` cannot be shadowed by a host file, but do not exercise a
  nested standalone-package volume, update publication, or multiple volume readers.
- The owning design is proposed and requires an upstream updater-layout qualification before the bootstrap layout is
  selected.

## Implementation Decisions

- Qualification first: no production store layout lands until the official updater succeeds from an isolated seed on
  both `linux/amd64` and `linux/arm64`.
- Shared contract: add `internal/codexinstall/` for protocol, target, names, labels, paths, release manifests, and pure
  validation. Host Docker orchestration stays in `launcher`; Linux filesystem mutation stays in `container`.
- Target source: derive `linux/<architecture>` from the Docker daemon, not `runtime.GOOS`, `runtime.GOARCH`, or a host
  Codex package. Images must match that target; explicit cross-architecture emulation remains unsupported.
- Trusted updater image: use the configured product base image and Docker's default runtime. Never use a
  project-derived image or pass project, Codex-home, credential, skill, cache, MCP, or Docker-socket mounts.
- Mount ownership: represent bind and named-volume mounts explicitly in `dockercli`; append the Codex volume after the
  broader Codex-home bind and never serialize it into `.agents-safe/config.toml` or `launchplan` project intent.
- Single publication commit: put protocol, target, version, entrypoint, and digest metadata inside each immutable
  release. Atomically replacing `current` is the only commit point; no required mutable metadata write follows it.
  Amend the proposed design before implementation to remove its current post-commit metadata step.
- Dispatcher shape: install the verified bootstrap outside the mounted store and make `/usr/local/bin/codex` invoke an
  image-owned Go dispatcher mode in `codex-safe-session`. The maintenance mode calls the staged upstream binary
  directly, so the ordinary-session `codex update` rejection cannot block maintenance.
- Writer coordination: use both the deterministic owned maintenance-container name and a Linux kernel file lock in
  the volume. The container name gives a useful host diagnostic; the lock protects publication after crashes/races.
- Fingerprint policy: increment the fingerprint schema and include mount kind, volume identity, target, protocol, and
  read-only mode. Exclude release version, release digest, and the `current` target.
- Test isolation: destructive publisher and corruption cases use uniquely named test volumes. The public Sysbox test
  may inspect or create the production-identity store, but must not overwrite or delete a pre-existing user store.
- Dependencies: add no Go module. Keep the image bootstrap pinned by checksum and document that later releases are an
  updater-managed runtime dependency verified and recorded by the maintenance helper.
- macOS boundary: `codex-safe update` bypasses the Linux/Sysbox session preflight and is accepted on Docker Desktop.
  Ordinary `codex-safe` project launches continue to report that the current session backend requires Linux.

## Phases

### Phase 1: Upstream Updater Qualification and Contract Closure
Purpose: Prove the supported upstream path and remove publication ambiguity before production code depends on it.
Status: to be done
Done when: both Linux targets have a recorded updater-compatible seed layout and the design has one atomic commit rule.

1. Run a bounded, networked Docker qualification for `linux/amd64` and `linux/arm64` using the pinned bootstrap and an
   isolated `CODEX_HOME`; do not expose host state or credentials.
2. Prove `codex update` recognizes the seed, installs a newer or idempotent current release, and leaves enough package
   metadata to identify version, target, and entrypoint.
3. Interrupt the updater before publication and verify that an external live-store fixture remains byte-for-byte
   unchanged.
4. Record exact image, source/target versions, target triples, package tree, commands, and results in
   `docs/reviews/feature-review/2026-07-21-persistent-codex-updater-qualification.md` without copying release binaries.
5. If the raw release binary is insufficient, select the smallest complete updater-compatible seed. Do not replace
   the official updater with a GitHub/latest-version scraper.
6. Amend the design so immutable release metadata plus atomic `current` replacement form the sole publication commit;
   define incomplete pre-commit files as unreachable staging/orphans, not an initialized live store.
7. Stop and revise the design if either target cannot use the official updater or the updater cannot be isolated from
   host-native packages.

### Phase 2: Store Identity and Typed Docker Transport
Purpose: Add fail-closed volume ownership and Docker target primitives without changing session behavior yet.
Status: to be done
Done when: the launcher can resolve one daemon target and safely create or adopt only its owned installation volume.

1. Add `internal/codexinstall/` constants and pure constructors for protocol version, supported Linux targets,
   deterministic volume/container names, labels, store paths, release manifests, and containment checks.
2. Add typed Docker daemon inspection that returns OS and architecture, rejects non-Linux daemons and unsupported
   architectures, and remains independent of the host operating system.
3. Add typed volume inspect/create operations and inspection structs to `internal/launcher/dockercli`; distinguish
   not-found from transport errors without parsing successful output loosely.
4. Implement launcher-side ensure logic that creates a labelled volume or validates every ownership label on an
   existing deterministic name. Never adopt, relabel, prune, or delete a mismatch.
5. Generalize Docker mount transport to encode explicit bind and volume kinds. Keep `rprivate` bind-only, preserve
   current bind argv byte-for-byte, and require read-only mode on session store requests.
6. Add an attached maintenance-container transport with streamed stdout/stderr, deterministic-name conflict
   detection, exit-code preservation, and owned cancellation cleanup.
7. Unit-test supported targets, UID/protocol/name boundaries, exact labels, malformed Docker inspection, volume-name
   conflicts, bind compatibility, volume argv, and attached-run failure/cancellation paths.
8. Update `internal/launcher/dockercli/README.md` only for the new transport ownership; do not move launcher policy into
   that package.

### Phase 3: Bootstrap Layout and Inactive Dispatcher
Purpose: Build and test executable selection before exposing a mutable store to normal sessions.
Status: to be done
Done when: the image contains a qualified bootstrap and a tested dispatcher mode that is not yet the public Codex path.

1. Change `container/Dockerfile` to install the qualified, checksum-pinned standalone seed below an image-owned
   bootstrap directory, retaining per-target version and executable checks.
2. Add store inspection and release validation in `internal/codexinstall/`: require a contained immutable release,
   matching protocol/target, regular executable entrypoint, and valid manifest/digest.
3. Add a Codex-dispatch mode to `cmd/codex-safe-session` that resolves `CODEX_HOME`, chooses a valid release or the
   bootstrap, blocks ordinary `codex update`, and replaces itself with the selected executable.
4. Treat a store with no committed `current` as empty even if unreachable staging/orphan directories exist. Treat an
   invalid committed target as corruption and fail without bootstrap fallback.
5. Keep `/usr/local/bin/codex` executing the bootstrap directly until Phase 4 installs the volume mount and activates
   the dispatcher in the same change.
6. Add pure and command tests for empty, valid, escaping, missing, wrong-target, wrong-protocol, non-regular,
   digest-mismatch, and malformed stores, plus argv/environment/exit-code preservation and update rejection.
7. Build both image targets and verify the bootstrap and inactive dispatcher helper report the expected target and
   version without reading a host Codex home.

### Phase 4: Read-Only Session Integration and Fingerprinting
Purpose: Make the persistent store the executable source for every session without coupling reuse to release version.
Status: to be done
Done when: all new sessions mount the owned store read-only and the public Codex path dispatches through it.

1. Resolve/validate the Docker daemon target and installation volume before container inspection, fingerprinting, or
   creation. Do not perform an update or network release lookup during normal launch.
2. Compute the mount target from the resolved Codex-home role, falling back to the ephemeral container home, then
   append the named volume after all broader bind mounts.
3. Activate `/usr/local/bin/codex` as the image-owned dispatcher entrypoint only in the same change that guarantees the
   narrower volume mount for both `codex-safe` and `agents-safe` sessions.
4. Increment the creation fingerprint schema and encode typed mounts plus store name, target, protocol, and read-only
   mode. Prove that changing `current` or release contents leaves the fingerprint unchanged.
5. Add diagnostic session labels for store name, target, and protocol, while keeping the fingerprint as the sole
   creation-time reuse predicate after ownership checks.
6. Replace project-image architecture comparison with daemon-target validation and keep entrypoint, root bootstrap,
   command, and private-Docker checks unchanged.
7. Add focused tests for custom/absent `CODEX_HOME`, parent-before-child ordering, host-package shadowing, project
   config overlap immunity, volume mismatch, existing-session mismatch, and routine-version reuse.
8. Prove session root cannot write the store mount in an ordinary Docker container before relying on the same mount
   flag in Sysbox acceptance.

### Phase 5: Isolated Update Command and Atomic Publisher
Purpose: Add the user-facing update flow and the store's only supported writer.
Status: to be done
Done when: `codex-safe update` publishes exactly one validated release or leaves the previous commit unchanged.

1. Refactor `cmd/codex-safe` around injectable command dependencies, following `agents-safe init`, and recognize a
   leading `update` before launcher flags, Git discovery, and project configuration.
2. Add strict `codex-safe update` help/argument parsing. Keep `codex-safe -- update` forwarded to Codex and return
   updater/container exit failures without converting them into usage errors.
3. Add launcher update orchestration that validates only update-required host identity and Docker fields, resolves the
   daemon target, ensures the owned volume and trusted base image, and never enters Linux/Sysbox session preflight.
4. Build the maintenance request with the volume read-write, a temporary home/staging area, target identity/protocol,
   default Docker runtime, and no project/user-state/cache/MCP/socket mounts or project-derived image.
5. Add a maintenance subcommand to `cmd/codex-safe-session` and implement Linux-only locking, isolated seed/update,
   staged validation, content-digest comparison, immutable directory rename, and atomic `current` replacement.
6. Make same-version/same-digest publication idempotent and same-version/different-digest publication fail closed.
   Once `current` swaps successfully, report success; do not perform required mutable writes after that commit.
7. On host cancellation or a failed attached run, inspect the deterministic maintenance name, validate ownership, stop
   only that owned container, and preserve the volume and previous `current`.
8. Add tests for first publication, no-op publication, interrupted download/copy, invalid package, full store, lock and
   name races, stale-name mismatch, signal cleanup, old-current preservation, and concise success/recovery diagnostics.

### Phase 6: Docker, Sysbox, and macOS Boundary Proof
Purpose: Verify multi-container behavior and platform separation at the real Docker mount/process boundary.
Status: to be done
Done when: isolated Docker tests cover mutation/failure and real hosts cover the supported Linux and macOS boundaries.

1. Add an opt-in ordinary-Docker suite and Make target using unique labelled volumes and the default runtime; keep it
   compiled/vetted but skipped during `make test` unless explicitly enabled.
2. Prove two read-only containers execute concurrently, container root cannot write, one maintenance writer can
   publish, and a later process selects the new release while an already-running process keeps the old one.
3. Race two updates and kill the winner at each failpoint around copy/rename/commit; verify one useful conflict
   diagnostic, lock release, no partial selected release, and preserved old `current`.
4. Mount a fake Darwin standalone tree through the parent Codex-home bind and prove the narrower Linux volume hides it
   in both empty-store bootstrap and published-release cases.
5. Extend the Sysbox smoke suite to prove the public session mount is a read-only named volume for container root,
   shared across two projects, and absent from nested Docker. Preserve any pre-existing production-identity volume.
6. On a compatible Linux host, run the focused ordinary-Docker suite, focused persistent-Codex Sysbox tests, and the
   complete `make test-smoke-go` gate.
7. On macOS Docker Desktop, build the trusted Linux image and run `codex-safe update`; prove the daemon-derived target,
   labelled volume reuse, no host package path, and no Sysbox/Git requirement. Do not claim project-session support.

### Phase 7: Documentation, Operations, and Review Handoff
Purpose: Align durable contracts and operating guidance with the verified implementation.
Status: to be done
Done when: users can update and recover the store from docs, all gates pass, and the plan is ready for owner review.

1. Update `README.md`, CLI usage, package READMEs, `ARCHITECTURE.md`, and `docs/makefile-reference.md` for the update
   command, store ownership, dispatcher, new module, and validation targets.
2. Update `docs/dependencies.md`: keep bootstrap pinning and document official-updater-managed releases, recorded
   manifests/digests, and the absence of a repository-owned latest-version service.
3. Add an operations runbook for inspecting labels/current/version, diagnosing corruption or an active updater,
   backing up the volume, and explicitly removing an owned unused store. Do not add automatic reset or pruning.
4. Mark the persistent-installation design implemented and reconcile `codex-safe.md`, project-image architecture text,
   session fingerprint contracts, testing docs, and design/operations catalogs with the final code.
5. Run an implementation review against the design, this plan, live diff, updater qualification, and Docker/Sysbox/macOS
   evidence; record fixes in one feature-review artifact.
6. Run every validation gate below, record exact environmental blockers without substituting unit evidence, and add
   dated progress notes including any qualification-driven layout change.
7. Set every non-cancelled phase to `done`, move this plan to `review/`, and update the execution-plan index only after
   the in-scope implementation and required available gates are complete.

## Validation Gates

- `gofmt` runs on every changed Go file and `git diff --check` passes after every phase.
- `go test ./internal/codexinstall ./internal/launcher/dockercli ./internal/launcher` passes after Phases 2, 4, and 5.
- `go test ./cmd/codex-safe ./cmd/codex-safe-session ./internal/container` passes after Phases 3 and 5.
- `go vet` passes for every changed Go package after its phase.
- Fixed Docker argv fixtures prove existing bind requests are unchanged and Codex store requests are `type=volume`,
  ordered after the Codex-home bind, and `readonly` only in normal sessions.
- Fingerprint fixtures prove the schema changes for store identity/target/protocol/mode and remains stable when only
  release version, digest, or `current` changes.
- Both qualified image targets build and their bootstrap/dispatcher probes report the expected Linux target.
- `make lint`, `make test`, `make docker-build`, and `make check-docs` pass before review handoff.
- The new ordinary-Docker target passes and proves atomic failure behavior against unique disposable volumes.
- Focused persistent-Codex Sysbox tests and `make test-smoke-go` pass on a compatible Linux/Sysbox host.
- A Docker Desktop macOS run proves `codex-safe update` without Git or Sysbox. If no compatible macOS host is
  available, record that exact acceptance blocker and do not describe cross-platform behavior as real-host verified.
- `docker volume inspect <resolved-name>` shows every required ownership label and no project-specific label.
- Session inspection shows the installation volume read-only; maintenance inspection shows the same volume read-write
  and no project, host Codex-home, credentials, skills, cache, MCP, or Docker-socket mount.
- `codex-safe -- update` reaches the ordinary dispatcher rejection, while leading `codex-safe update` never calls Git
  discovery and succeeds from outside a repository.

## Risks and Constraints

- The official updater's accepted installation layout is an upstream behavior, not a complete public contract. The
  qualification gate must block implementation when that behavior changes.
- Updater-managed releases intentionally move outside repository checksum pinning. The maintenance helper must retain
  upstream validation, record the installed digest, and never treat an unvalidated download as published.
- Docker local volumes provide the required same-daemon rename and locking semantics. Remote drivers, NFS, Swarm, and
  Kubernetes volumes are unsupported until independently qualified.
- Killing the Docker CLI must not leave an unobserved maintenance container writing in the background. Cancellation
  cleanup may act only after deterministic-name ownership validation.
- Published releases accumulate because safe garbage collection requires proof that no process can late-load files
  from an old release. This plan must not add age-based cleanup.
- The current Linux launcher preflight and Sysbox backend remain host-limited. Only the maintenance path is expected to
  run on macOS in this implementation.
- A Docker daemon architecture is the native target only. Explicit emulation, multi-platform manifests selected per
  command, and cross-daemon sharing require a new design.
- The persistent store is executable shared state. Any accidental read-write session mount is a security regression,
  not a recoverable convenience.
- Do not add or update any dependency without explicit owner approval.

## Out of Scope

- A macOS project-session backend, Docker Desktop replacement for Sysbox, Windows support, or remote-daemon UX.
- Executing, copying, repairing, or migrating host-native Codex packages.
- Automatic update checks at session startup, background updates, arbitrary version selection, downgrade, channels,
  rollback commands, or a repository-owned latest-release API.
- Release garbage collection, quotas, volume pruning, destructive reset automation, or store migration across protocol
  versions, Docker daemons, UIDs, or architectures.
- npm/Node installation, a new Go module, production image publication/signing, or changes to Codex auth/config/session
  persistence.
- Writable store access from project sessions, relay sidecars, project-image builds, nested Docker, or host MCP.

## Progress Notes

- 2026-07-21: Created from the proposed persistent-installation design and the live image, launcher, Docker transport,
  fingerprint, container-entrypoint, and smoke-test baselines. Implementation has not started.
