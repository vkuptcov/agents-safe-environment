# Exec Plan: Persistent Container Codex Installation

- Status: in review
- Created: 2026-07-21
- Design: [Persistent Container Codex Installation and Updates](../../design-docs/persistent-codex-installation.md)
- Scope:
  - `cmd/codex-safe/`, `internal/launcher/`, and `internal/launcher/dockercli/`
  - `container/`
  - launcher, Docker, image, and documentation tests
  - `README.md`, `ARCHITECTURE.md`, and owning docs

## Objective

Make routine container Codex updates a single `codex-safe update` operation without rebuilding project images. Keep
the implementation small by delegating release layout, checksums, locking, and publication to OpenAI's official
standalone installer.

## Done Criteria

- `codex-safe update` runs without Git discovery or the Sysbox session preflight.
- All sessions share one daemon-local Codex volume and mount it read-only.
- The maintenance container is the only supported read-write path and receives no project or host Codex state.
- The image dispatcher uses the volume installation when available and the pinned bootstrap otherwise.
- `codex-safe -- update` is forwarded and rejected inside the session with a host-update diagnostic.
- Focused Go tests, repository gates, image build, disposable-volume Docker proof, and docs checks pass or have an
  explicit environment blocker.

## Current Baseline

The image pins one raw Codex binary at `/usr/local/bin/codex`, so every update requires an image rebuild. The first
implementation attempt added a custom store protocol, manifests, digest validation, daemon/volume CRUD, and publisher
orchestration before a user-visible update command existed. The official installer already provides the package
layout and update mechanics that code attempted to reproduce.

## Implementation Decisions

- Official installer: run `https://chatgpt.com/codex/install.sh` with its documented non-interactive environment.
- One volume: use `codex-safe-codex` per Docker daemon; store executable packages only.
- Separate path: mount the volume at `/opt/codex-safe/codex`, not under host `CODEX_HOME`.
- Simple dispatcher: select the installer-created alias when executable, otherwise use the pinned image bootstrap.
- Docker-native creation: let `docker run --mount type=volume` create an absent volume; add no volume CRUD API.
- Trusted writer: use the default base image, default Docker runtime, one read-write volume, and no sensitive binds.
- Minimal concurrency: use the deterministic maintenance-container name plus the official installer lock.
- Reuse boundary: bump the launch-fingerprint schema once for the new session mount; exclude release contents.
- Dependency: add owner-approved `curl` to the image for the official installer.

## Phases

### Phase 1: Simplify the Contract
Purpose: Remove repository-owned behavior already provided by the official standalone installer.
Status: done
Done when: the design has no custom release protocol, publisher, manifest, daemon identity, or volume-adoption layer.

1. Replace the original design with the single-volume official-installer contract.
2. Remove the unused `internal/codexinstall` package and daemon/volume CRUD transport.
3. Reduce named-volume transport to explicit bind and volume mount lists.

### Phase 2: Implement Update and Selection
Purpose: Deliver the host command, trusted writer, and read-only reader path.
Status: done
Done when: a new session can execute either the installed release or bootstrap, and the host command updates the
shared volume without project discovery.

1. Add `codex-safe update` dispatch before the shared launcher CLI.
2. Add the attached default-runtime maintenance request with only the writable volume.
3. Add the read-only volume to every session create request and bump the fingerprint schema.
4. Install the dispatcher, updater wrapper, bootstrap path, and `curl` in the image.

### Phase 3: Tests and Documentation
Purpose: Prove the simplified boundaries and make the operational behavior discoverable.
Status: done
Done when: unit tests cover dispatch/mount/exit behavior and current docs describe the implemented command.

1. Test command routing, strict update arguments, and exit-code propagation.
2. Test exact read-only session and read-write maintenance Docker argv.
3. Test dispatcher fallback, updated selection, and direct-update rejection in the built image.
4. Update README, architecture, dependency, testing, and package documentation.

### Phase 4: Runtime Verification and Handoff
Purpose: Verify the real Docker boundary and leave the plan ready for review.
Status: done
Done when: available gates pass, unavailable real-host checks are recorded exactly, and the plan is in review.

1. Build the image and run the official installer against a uniquely named disposable volume.
2. Prove a read-only container executes the installed release and cannot update it directly.
3. Run the repository test, lint, Docker-build, and docs gates.
4. Run Sysbox smoke tests when the runtime is available; record the known runtime blocker otherwise.
5. Record Docker Desktop macOS verification as passed only after a real macOS run.
6. Move this plan to `review/` after implementation and all available gates complete.

## Validation Gates

- `go test ./cmd/codex-safe ./internal/launcher/dockercli ./internal/launcher` passes.
- `go vet ./cmd/codex-safe ./internal/launcher/dockercli ./internal/launcher` passes.
- `sh -n container/codex-dispatcher container/codex-safe-update` passes.
- `make lint` and `make test` pass.
- `make docker-build` passes and `/usr/local/bin/codex --version` uses the pinned bootstrap in an empty volume.
- A disposable-volume Docker run of `/usr/local/bin/codex-safe-update` succeeds, and a later read-only run reports the
  installed version through `/usr/local/bin/codex`.
- Session Docker argv contains `type=volume,source=codex-safe-codex,target=/opt/codex-safe/codex,readonly`.
- Maintenance Docker argv contains the same volume without `readonly`, no bind mounts, no Sysbox runtime, and no
  Docker socket.
- `make test-smoke-go` passes on a compatible Sysbox host or its environment blocker is recorded.
- `make check-docs` passes.
- A Docker Desktop macOS run of `codex-safe update` is recorded before claiming real-host macOS verification.

## Risks and Constraints

- The updater executes a freshly downloaded official install script; that script and its release endpoints are part
  of the trusted update channel.
- A Docker client interrupted by context cancellation can leave the maintenance container running until the installer
  exits. Its deterministic name prevents a second writer meanwhile.
- The volume is daemon-wide. Users with Docker access can replace it, but they already control the images and
  containers in this trust boundary.
- The pinned bootstrap remains older until the image itself is refreshed; it is recovery behavior, not the normal
  update source.
- The current project-session backend still requires Linux and Sysbox.

## Out of Scope

- A custom package protocol, repository-owned release API, manifests, rollback, pruning, or repair tooling.
- Version arguments, downgrade, channels, automatic checks, or background updates.
- Per-project or per-user executable volumes.
- A macOS project-session backend, Windows support, or explicit cross-architecture emulation.

## Progress Notes

- 2026-07-21: Replaced the original seven-phase custom publisher plan with the official standalone installer. Removed
  the unused store protocol, release manifests, daemon target inspection, volume CRUD, and special cancellation
  cleanup. The raw image binary was verified not to support self-update by itself; the official installer path is
  therefore required.
- 2026-07-21: The disposable-volume Docker proof selected bootstrap `0.144.4`, installed official release `0.145.0`,
  selected it through a read-only mount, rejected direct update, and denied a root write. The test volume was removed.
- 2026-07-21: Focused Go tests/vet, `make lint`, `make test`, `make docker-build`, `make test-smoke-go`, and
  `make check-docs` passed. A real Docker Desktop macOS host was unavailable, so macOS compatibility is implemented
  but not real-host verified.
