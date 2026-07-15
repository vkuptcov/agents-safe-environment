# Exec Plan: Run Codex Inside the Container

- Status: active
- Created: 2026-07-15
- Design: [`docs/design-docs/codex-safe.md`](../../design-docs/codex-safe.md)
- Scope:
  - `cmd/codex-safe/`, `internal/launcher/`
  - `container/Dockerfile`
  - `tests/smoke/`
  - `README.md`, `docs/design-docs/`

## Objective

Turn the infrastructure launcher into the product `codex-safe`: install a pinned Codex CLI in the outer image, resolve
and mount the host Codex home and personal skills, and run interactive Codex by default inside the existing Sysbox
session.

This plan implements the `Codex Agent Integration` contract in the design doc (§1, §3, §4, §10). It builds on the
detached session manager and does not change the registration protocol, nested-Docker isolation, or identity mounts.

## Done Criteria

- `codex-safe` with no arguments starts interactive Codex for the current Git project.
- Arguments after `--` are forwarded to Codex unchanged; the launcher never runs an arbitrary executable.
- The launcher resolves the Codex home from `CODEX_HOME` or `~/.codex`, validates it, and mounts it read-write at
  `$HOME/.codex` with container `CODEX_HOME` set to that path.
- `$HOME/.agents/skills` is mounted read-only when present and recorded as `absent` otherwise.
- A personal-skills source overlapping a writable mount (worktree, common Git directory, or Codex home) is rejected at
  preflight.
- The image provides a pinned Codex CLI at an image-owned path that the mounted Codex home cannot shadow.
- Reuse validates the `codex-safe.codex-home` and `codex-safe.personal-skills` labels; a different user-state for the
  same worktree gets the finish-active-session diagnostic and neither reuses nor terminates the live container.
- Codex reads sentinel config, global instructions, and a skill and writes session state back to the host with usable
  ownership (smoke).
- Personal skills are readable but not writable, and external symlink targets stay unavailable (smoke).
- Required Go, image, and Sysbox smoke gates pass; each phase is committed separately.

## Current Baseline

The MVP and the session manager provide a detached Sysbox container with the Go `serve`/`run` wrapper, deterministic
naming, host-identity parity, `.gitconfig`, host-home-path parity, sudo, terminal defaults, and Compose tooling.

Codex is not installed, no host Codex home is mounted, and the CLI still requires an explicit probe command
(`codex-safe -- COMMAND`). The `serve`/`run` wrapper is already command-agnostic, so running Codex is a launcher and
image change, not a protocol change.

The design doc §4 now specifies Codex-home resolution, personal skills, the product command, and the authentication
boundary; `go-session-manager.md` §1 defines `codex-safe.codex-home` and `codex-safe.personal-skills`, but no launcher
code sets or validates them yet.

## Implementation Decisions

- Product CLI: no arguments run interactive Codex; arguments after `--` are Codex arguments, not an arbitrary
  executable. Keep `--project`, `--image`, and `--help`.
- Codex invocation: run Codex by its absolute image-owned path (for example `/usr/local/bin/codex`), never the bare
  name, so a mounted Codex home cannot shadow it through `PATH`.
- Wrapper unchanged: keep `codex-safe-session run` command-agnostic; the launcher only changes which command it wraps.
- Container Codex path: always set `CODEX_HOME=<container-home>/.codex` regardless of the host source, mounted
  read-write at `$HOME/.codex`.
- Personal skills: mount `$HOME/.agents/skills` read-only; encode absence as the literal `absent`; never mount the
  broader `$HOME/.agents` or follow external symlink targets.
- Fail closed: a missing, relative, root, non-directory, unreadable, or unwritable Codex home fails preflight; never
  create it and never fall back to another location.
- Reuse labels: extend the existing labels with `codex-safe.codex-home` and `codex-safe.personal-skills`. Ownership or
  protocol mismatch stays a name conflict; a user-state mismatch uses the finish-active-session diagnostic.
- Env forwarding: extend the existing allowlist with `CODEX_HOME` only; do not copy the host environment or forward
  API keys implicitly.
- Codex install: install the standalone musl release from GitHub Releases, pinned to `rust-v0.144.4` and verified by
  per-arch SHA-256, to `/usr/local/bin/codex`. Chosen over the npm package to keep Node out of the image and pin one
  checksummed artifact per [`docs/dependencies.md`](../../dependencies.md); updates require a new image build with no
  network install at startup.
- Personal-skills isolation: reject a canonical skills source that overlaps any writable mount (worktree, common Git
  directory, or Codex home); a read-only mount is not trusted when the same source is writable through another target.
- Smoke transport: drive arbitrary probe commands through a build-tagged, test-only launcher that is absent from the
  product binary; the product `codex-safe` runs only Codex.
- Host dependencies: Go standard library only; no Docker SDK or RPC framework.

## Dependency Approval

The pinned Codex CLI is a new container dependency and requires owner approval before Phase 2 per
[`docs/dependencies.md`](../../dependencies.md). Phase 2 must not start until this record is present.

- Dependency: Codex CLI standalone release, invoked at `/usr/local/bin/codex`.
- Release and version: `rust-v0.144.4`; `codex --version` reports `codex-cli 0.144.4`.
- Source: `openai/codex` GitHub Releases; digests taken from the release asset metadata.
- Approved assets and digests:
  - `amd64` -> `codex-x86_64-unknown-linux-musl.tar.gz`
    - SHA-256: `37c985be9d89e8c4f43b3aa0594c1213eac212d30ae2b95221f08fec807515d1`
  - `arm64` -> `codex-aarch64-unknown-linux-musl.tar.gz`
    - SHA-256: `4d07243ef4ae6786b8b321d7aea3f9be4e1d2c597ae5407e7c1b9873334082b2`
- Owner approval: recorded 2026-07-15; the repository owner directed the standalone musl choice and pinned this
  version in the planning session. Re-approval is required before bumping the version or digests.

## Phases

### Phase 1: Codex Home and Personal-Skills Resolution

Purpose: Resolve and validate the two host user-state sources as pure launcher logic.
Status: to be done
Done when: the launcher resolves and rejects Codex-home and personal-skills sources correctly, including a
writable-alias overlap, without starting Docker.

1. Add `internal/launcher/userstate.go` resolving the Codex home from `CODEX_HOME`, else `~/.codex` below the
   OS-resolved home.
2. Canonicalize the source and require an existing, accessible, read-write directory; reject missing, relative, root,
   and non-directory values.
3. Resolve optional `$HOME/.agents/skills`: canonicalize when it exists, otherwise mark it absent; never follow
   external symlink targets.
4. Reject a canonical personal-skills source that overlaps any writable mount source (worktree root, linked-worktree
   common Git directory, or Codex home), so a read-only skill cannot be modified through a writable alias.
5. Record the overlap-rejection rule in the design-doc §4 personal-skills contract in the same change.
6. Return typed results carrying the canonical sources and the personal-skills absence marker.
7. Add `internal/launcher/userstate_test.go` for default and explicit `CODEX_HOME`, symlinked sources, invalid values,
   present and absent skills, and literal and symlinked writable-alias overlap.

Commit: `feat: resolve codex home and personal skills`

### Phase 2: Codex CLI in the Outer Image

Purpose: Ship a pinned, image-owned Codex CLI before any launcher path depends on it.
Status: to be done
Precondition: the owner approval in `Dependency Approval` is recorded.
Done when: the image contains the pinned Codex at `/usr/local/bin/codex` and reports its version without a network
install.

1. In `container/Dockerfile`, add a build stage that downloads the approved release asset and verifies its SHA-256,
   following the existing per-arch `crun` pinning pattern.
2. Map Docker `TARGETARCH` to the approved asset in `Dependency Approval` and verify each against its approved digest:
   - `amd64` -> target `x86_64-unknown-linux-musl`, asset `codex-x86_64-unknown-linux-musl.tar.gz`;
   - `arm64` -> target `aarch64-unknown-linux-musl`, asset `codex-aarch64-unknown-linux-musl.tar.gz`.
3. Download from `https://github.com/openai/codex/releases/download/rust-v0.144.4/<asset>`.
4. Extract the single archive entry `codex-<target>`, install it mode `0755` at `/usr/local/bin/codex`, and keep that
   path outside any bind-mount target.
5. Do not install or update Codex from the network at container startup.
6. Add a container-run check that `/usr/local/bin/codex --version` prints `codex-cli 0.144.4`.

Commit: `feat: install pinned codex cli in image`

### Phase 3: Test-Only Smoke Transport

Purpose: Preserve real-host lifecycle coverage before the product CLI stops accepting arbitrary commands.
Status: to be done
Done when: every current smoke probe reaches the command-agnostic session wrapper through a test-only transport that
is absent from the product binary.

1. Add a build-tagged, test-only launcher entry (for example `bin/codex-safe-probe` from a `//go:build smoke` main)
   that drives the existing create, reuse, and exec path with an arbitrary command through `codex-safe-session run`.
2. Migrate the environment, worktree, nested-Docker, reuse, and concurrency probes in `tests/smoke/` from
   `codex-safe -- COMMAND` to that transport.
3. Keep every existing lifecycle, reuse, linked-worktree, nested-Docker, Compose, Cyrillic, and color assertion
   passing through the new transport.
4. Prove the product `codex-safe` binary exposes no arbitrary-command execution surface.

Commit: `test: route smoke probes through a test-only transport`

### Phase 4: User-State Mounts, Environment, and Default Codex Command

Purpose: Put Codex home and skills into the mount plan and make Codex the default wrapped command.
Status: to be done
Done when: tests prove the plan mounts both user-state sources and the product CLI runs the image-owned Codex by
default through the wrapper.

1. Add the read-write Codex-home mount at `$HOME/.codex` and the read-only `$HOME/.agents/skills` mount to the shared
   mount set used by regular and linked worktrees.
2. Set container `CODEX_HOME=<container-home>/.codex` alongside the existing `HOME`, and add `CODEX_HOME` to the env
   allowlist.
3. Change `cmd/codex-safe/main.go`: default to interactive Codex, forward post-`--` arguments as Codex arguments, keep
   `--project`, `--image`, and `--help`, and stop executing arbitrary commands.
4. Build the default command as the absolute image-owned Codex path plus forwarded arguments, prefixed by the
   `codex-safe-session run --` wrapper.
5. Extend launcher and CLI tests for mount modes and targets, `CODEX_HOME`/`HOME`, default argv, forwarded arguments,
   the unchanged wrapper prefix, and rejection of an arbitrary executable.

Commit: `feat: run codex by default with user-state mounts`

### Phase 5: User-State Reuse Labels and Mismatch Diagnostic

Purpose: Make container reuse honor the Codex-home and personal-skills sources.
Status: to be done
Done when: a differing Codex home or skills source for the same worktree is rejected with the active-session
diagnostic instead of reused.

1. Add `codex-safe.codex-home` and `codex-safe.personal-skills` labels on create, using the `absent` marker when no
   skills directory exists.
2. On an existing running deterministic name, validate both user-state labels after the ownership and protocol labels.
3. Keep ownership or protocol mismatch as a name conflict; route a user-state mismatch to a finish-active-session
   diagnostic that neither reuses nor terminates the live container.
4. Extend launcher tests for label construction, matching reuse, the absent marker, and the user-state mismatch
   diagnostic.

Commit: `feat: validate user-state labels on reuse`

### Phase 6: Real-Host Codex Smoke Proof

Purpose: Prove Codex runs from a mounted host state directory with the intended isolation on a real Sysbox host.
Status: to be done
Done when: a live session runs Codex from a sentinel home and proves state round-trip, read-only skills, shadowing
rejection, and the reuse-mismatch behavior beside the migrated transport.

1. Create a temporary sentinel Codex home with `config.toml`, global instructions, and a Codex-managed skill; use no
   real credential.
2. Launch Codex through the product CLI and prove it reads the sentinel state and writes session state back to the
   host with host-editable ownership.
3. Mount a personal-skills directory and prove it is readable, not writable, and that an external symlink target stays
   unavailable.
4. Place a host standalone Codex binary under the mounted state and prove it cannot shadow the image-owned executable.
5. Relaunch the same worktree with a different Codex home and prove the live container is not reused or terminated and
   the active-session diagnostic is reported.
6. Add an opt-in credentialed acceptance test that uses a dedicated test account only; keep real credentials out of
   fixtures, logs, and CI artifacts.

Commit: `test: prove codex runs in a sysbox session`

### Phase 7: Documentation and Review Handoff

Purpose: Make the product launcher usage accurate and ready for owner review.
Status: to be done
Done when: user documentation matches verified behavior, all validation gates pass, and this plan moves to review.

1. Update `README.md` for the product `codex-safe` usage: no-argument default, `--` forwarding, Codex-home resolution,
   personal skills, and the authentication boundary.
2. Document the pinned Codex version and the new-image-build update policy.
3. Update design docs only for implementation deviations beyond the §4 overlap rule already recorded in Phase 1.
4. Run every validation gate below and record dated results in `Progress Notes`.
5. Set completed phase statuses to `done`, set plan status to `in review`, and move this file to `review/`.

Commit: `docs: document codex product launcher`

## Validation Gates

- `gofmt -l cmd internal` prints no paths.
- `go test ./internal/launcher ./cmd/codex-safe` passes.
- `go test -race ./internal/launcher` passes.
- `go test ./internal/launcher` covers writable-alias overlap rejection for a personal-skills source.
- A `cmd/codex-safe` unit test proves post-`--` arguments are forwarded to the Codex argv and never executed as a
  standalone command.
- `make test` passes all Go tests, vet checks, and shell syntax checks.
- `make docker-build` builds `codex-safe-mvp:local` with the pinned Codex installed.
- `docker run --rm --entrypoint /usr/local/bin/codex codex-safe-mvp:local --version` prints the pinned Codex version.
- `rg -n "/usr/local/bin/codex" internal/launcher` shows Codex is invoked by an absolute image-owned path, not a bare
  name.
- `make test-smoke-go` passes on a Sysbox host through the test-only transport and proves Codex-home state round-trip,
  read-only personal skills, shadowing rejection, and user-state reuse mismatch.
- After the smoke test, `docker ps -a --filter label=codex-safe.managed=true --quiet` prints nothing.
- `make check-docs` passes for README and design-doc changes.
- `git diff --check` reports no whitespace errors.
- All Markdown lines changed by the implementation are at most 120 characters.

## Risks and Constraints

- Codex CLI is a new container dependency; Phase 2 is gated on the recorded owner approval in `Dependency Approval`.
  Re-approve before bumping the version or digests.
- Flipping the product CLI removes the arbitrary-command surface the current smoke harness uses. Phase 3 must migrate
  every probe to the test-only transport before Phase 4, or real-host lifecycle coverage is lost.
- Codex's real `CODEX_HOME` layout, interactive login, and auth store must be verified against Codex docs; unit tests
  cannot prove Codex behavior, so the Phase 6 smoke gate is mandatory.
- The go-session-manager plan is still active and owns the four identity and protocol labels; this plan adds only the
  two user-state labels. Land after or coordinate with that cutover to avoid label-set churn.
- Interactive authentication cannot run unattended; keep it in the opt-in credentialed acceptance test with a
  dedicated account and never commit real credentials.
- Shadowing protection depends on invoking Codex by absolute path and keeping the image path outside any mount target;
  prove it in smoke.
- Sysbox UID/GID behavior governs whether Codex session state written in the container stays host-editable; prove host
  ownership on the supported filesystem.

## Out of Scope

- CPU, memory, PID caps (`--cpus`, `--memory`, `--pids-limit`) and the rest of §8 resource policy — a separate plan.
- Keyring or keychain credential reuse, SSH agent forwarding, Git credential helpers, and cloud credential mounts.
- The session-manager registration protocol and its four identity and protocol labels.
- Automatic translation of host-specific hooks, MCP commands, skill scripts, or plugins for the Linux container.
- Network policy, egress allowlists, and host port publishing.
- Persistent nested Docker images, volumes, build cache, and disk quotas.
- Non-Linux hosts, Docker Desktop, rootless host Docker, and remote Docker daemons.

## Progress Notes

- Add dated notes before moving this plan to `review/`, including validation results, phase commits, deviations from
  the plan above, and any intentionally deferred work.
