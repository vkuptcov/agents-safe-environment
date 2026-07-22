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

Keep one Linux Codex installation in the daemon-local volume. Initialize it after the base image build and update it
later through `codex-safe update`, without rebuilding project images or retaining a second bootstrap binary.

## Done Criteria

- `codex-safe update` runs without Git discovery or the Sysbox session preflight.
- All sessions share one daemon-local Codex volume and mount it read-only.
- The maintenance container is the only supported read-write path and receives no project or host Codex state.
- `make docker-build` initializes or updates the shared volume after building the local image.
- The launcher executes only the absolute volume installation path; the image contains no Codex dispatcher.
- `codex-safe -- update` is forwarded directly to the read-only volume installation and is not a supported writer.
- Focused Go tests, repository gates, image build, disposable-volume Docker proof, and docs checks pass or have an
  explicit environment blocker.

## Current Baseline

The simplified first implementation still keeps two Codex binaries: a checksum-pinned bootstrap in the image and the
routine installation in `codex-safe-codex`. This duplicates the executable solely to recover from an empty volume,
even though this repository distributes a local image through a networked `make docker-build` workflow.

## Implementation Decisions

- Official installer: run `https://chatgpt.com/codex/install.sh` with its documented non-interactive environment.
- One volume: use `codex-safe-codex` per Docker daemon; store executable packages only.
- Separate path: mount the volume at `/opt/codex-safe/codex`, not under host `CODEX_HOME`.
- Single executable location: the launcher invokes only the installer-created alias in `codex-safe-codex`.
- Build initialization: `make docker-build` builds the image, then runs its maintenance entrypoint against the volume.
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
Done when: a new session executes the installed release, and the host command updates the shared volume without
project discovery.

1. Add `codex-safe update` dispatch before the shared launcher CLI.
2. Add the attached default-runtime maintenance request with only the writable volume.
3. Add the read-only volume to every session create request and bump the fingerprint schema.
4. Install the updater wrapper and `curl` in the image and invoke the volume executable by absolute path.

### Phase 3: Tests and Documentation
Purpose: Prove the simplified boundaries and make the operational behavior discoverable.
Status: done
Done when: unit tests cover dispatch/mount/exit behavior and current docs describe the implemented command.

1. Test command routing, strict update arguments, and exit-code propagation.
2. Test exact read-only session and read-write maintenance Docker argv.
3. Test the volume installation, empty-volume failure, and direct absolute executable path in the built image.
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

### Phase 5: Keep Codex Only in the Volume
Purpose: Remove the redundant image bootstrap while preserving explicit updates and read-only session reuse.
Status: done
Done when: the base-image build initializes the volume, and no Codex executable remains in the image layers.

1. Remove the pinned Codex download, checksum, bootstrap installation, and fallback branch from the image.
2. Make `make docker-build` run the isolated updater after the image build.
3. Remove the dispatcher and invoke the volume executable directly by absolute path.
4. Update tests and durable documentation for the single-location contract.
5. Run the focused, repository, Docker, documentation, and real Sysbox gates.

## Validation Gates

- `go test ./cmd/codex-safe ./internal/launcher/dockercli ./internal/launcher` passes.
- `go vet ./cmd/codex-safe ./internal/launcher/dockercli ./internal/launcher` passes.
- `sh -n container/codex-safe-update` passes.
- `make lint` and `make test` pass.
- `make docker-build` passes and a read-only session-style mount executes Codex from `codex-safe-codex`.
- A disposable-volume Docker run of `/usr/local/bin/codex-safe-update` succeeds, and a later read-only run reports the
  installed version through `/opt/codex-safe/codex/bin/codex`.
- An empty disposable volume has no executable at the documented absolute path and selects no image binary.
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
- Removing or pruning the volume makes Codex unavailable until `codex-safe update` or `make docker-build` succeeds.
- `make docker-build` now has a networked runtime side effect after the image itself has been built successfully.
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
- 2026-07-22: Removed the pinned bootstrap and then removed the dispatcher after owner feedback. The launcher and
  interactive `PATH` now resolve the installer-created volume executable directly. `make docker-build` builds the
  image and updates the only Codex installation in the daemon-local volume.
- 2026-07-22: Focused tests, `make lint`, `make test`, `make docker-build`, disposable empty/install/read-only proofs,
  `make check-docs`, and the complete `make test-smoke-go` suite passed. Credentialed acceptance remained skipped
  because no test credentials were supplied; a real Docker Desktop macOS run remains unavailable.
