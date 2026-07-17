# Exec Plan: Project-Specific Agent Environments

- Status: active
- Created: 2026-07-17
- Intended executor: GPT-5.6-Terra
- Design: [`docs/design-docs/project-environments.md`](../../design-docs/project-environments.md)
- Scope:
  - `internal/cli/`, `cmd/codex-safe/`, `cmd/agents-safe/`, and `internal/testutil/clitest/`
  - `internal/launcher/`, new `internal/launcher/projectenv/`, and `internal/launcher/dockercli/`
  - `tests/smoke/`
  - `README.md`, `ARCHITECTURE.md`, and owning documentation

## Objective

Let a Git project add system toolchains through `.agents-safe/Dockerfile` while preserving the existing launcher,
Sysbox, mount, host-MCP, and session-lifecycle boundaries. A normal launch should confirm and build a missing project
image, reuse a valid cached image, and reject stale active environments without a fallback.

## Done Criteria

- Both public launchers automatically use a valid `.agents-safe/Dockerfile` when `--image` was not explicit.
- A missing project Dockerfile preserves the current base-image behavior and call order.
- A new or changed definition is confirmed before host Docker executes it.
- Build context is limited to `.agents-safe/`, and build output stays on the diagnostic stream.
- A valid cached project image avoids both confirmation and rebuild.
- Derived images are checked against the base image's startup and required-binary contract.
- Active-session reuse follows the project-environment label rules in the design.
- Build, context-change, image-change, and compatibility failures create no session and use no fallback image.
- Focused unit tests and a dependency-free real-Sysbox fixture prove the behavior.
- Required documentation and package maps describe the implemented contract.
- `make lint`, `make test`, `make docker-build`, `make check-docs`, and `make test-smoke-go` pass.

## Current Baseline

- `cmd/codex-safe/main.go` and `cmd/agents-safe/main.go` hard-code `codex-safe-mvp:local` as the default image.
- `internal/cli.Run` always passes an image string and does not preserve whether `--image` was explicitly supplied.
- `launchplan.Options` currently carries only `NoHostMCP`.
- `DockerLauncher.Launch` resolves user mounts and host MCP state, then `acquireContainer` inspects the deterministic
  container before any image preflight. A matching live session therefore performs only inspect plus exec.
- On the create path, `dockercli.Preflight` checks `sysbox-runc`, ensures the selected image is local, and may pull it.
- `ResolveImageID` exists, but the create path pins an immutable ID only when host-MCP forwarding needs the session
  and relay sidecar to share one image.
- Container reuse validates ownership, user-mount, and host-MCP labels. It has no project-environment label.
- `dockercli` has typed inspect, create, exec, and stop operations, but no typed image build or image-config probe.
- The base runtime image does not retain the Go compiler used in its `session-builder` stage.
- The public smoke harness always supplies `--image codex-safe-mvp:local` and has no project-image fixture.
- There is no tracked `.agents-safe/` directory and no active execution plan before this document.

## Implementation Decisions

- **One project contract:** version 1 recognizes only `.agents-safe/Dockerfile`; do not add a manifest or hooks.
- **Fixed build context:** hash and pass the entire `.agents-safe/` directory; reject symlinks and non-regular entries.
- **Explicit-image precedence:** record explicit `--image` intent in `launchplan.Options`; do not infer it from value
  equality with the default image.
- **Two identities:** the definition digest drives active-session compatibility; definition digest plus immutable base
  image ID drives the cached-image key.
- **No trust database:** a matching, compatible cached image is the record of a previous accepted build.
- **Fail-closed prompt:** only `y` or `yes` permits a missing cache-key build; non-interactive input fails.
- **No stdout pollution:** prompts, diagnostics, and Docker build progress use `DockerLauncher.Stderr`.
- **Immutable create input:** after validation, create the project session and any host-MCP sidecar from the resolved
  derived image ID, not its local tag.
- **No build lock in version 1:** concurrent cold callers may duplicate build work; the existing deterministic
  container-name race remains the correctness lock.
- **Detect mutable inputs:** re-read the project definition and base image ID after build; fail if either changed.
- **No new Go module:** use the standard library for traversal, hashing, and validation.
- **No repository dogfood image in this plan:** the design's `build-essential` example needs separate explicit
  dependency approval. Use a dependency-free smoke Dockerfile instead.

## Execution Protocol for GPT-5.6-Terra

1. Re-read the design, `ARCHITECTURE.md`, `docs/testing.md`, and package READMEs before editing code.
2. Inspect `git status` before every phase and preserve unrelated owner changes.
3. Mark exactly one phase `in progress`, implement only that phase, run its focused gate, then mark it `done`.
4. Do not add modules, container packages, host mounts, startup hooks, cache volumes, or new CLI flags.
5. Keep Docker argument construction typed; never construct production Docker commands through a shell string.
6. Update existing tests rather than weakening assertions that encode current call order or security boundaries.
7. If a phase reveals that the accepted design must change, stop and update the design before continuing.
8. After implementation, move this plan to `review/`, set `Status: in review`, update the index, and do not move it
   to `completed/` until owner or review acceptance.

## Phases

### Phase 1: Project Definition Model
Purpose: Produce one deterministic, validated description of `.agents-safe/` without invoking Docker.
Status: to be done
Done when: callers can distinguish absent and valid definitions, and invalid or mutable context entries fail closed.

1. Create `internal/launcher/projectenv/README.md` with the package boundary and non-test file map.
2. Add `environment.go` with constants for `.agents-safe`, `Dockerfile`, contract version, and label values.
3. Add a `Definition` value containing context path, Dockerfile path, and full definition digest.
4. Implement discovery from an already-canonical project root using `os.Lstat` and `filepath.WalkDir`.
5. Reject symlinks, devices, sockets, FIFOs, and entries that escape the `.agents-safe/` tree.
6. Hash sorted relative paths, entry types, executable bits, and regular-file contents; omit timestamps and ownership.
7. Add cache-key and deterministic local-image-name helpers using definition digest, base image ID, and project key.
8. Add `environment_test.go` covering absence, spaces, digest stability, content/mode changes, and every rejection.

### Phase 2: Typed Docker Image Operations
Purpose: Give the launcher typed build, inspect, and probe operations without moving policy into `dockercli`.
Status: to be done
Done when: tests prove the exact Docker argv and typed results needed to build and validate one project image.

1. Add `BuildRequest` and `ImageInspection` transport types to `internal/launcher/dockercli/request.go`.
2. Add `build.go` with `BuildArgs` and `Build`, passing file, tag, `AGENTS_SAFE_BASE`, ordered labels, and context.
3. Route both Docker build output streams to the caller-provided diagnostic writer and retain bounded failure text.
4. Extend image inspection to return ID, architecture, user, entrypoint, command, environment, and labels.
5. Add a one-shot image probe operation that accepts an immutable ID, entrypoint, and argv.
6. Encode probes with `--rm`, `--network=none`, `--read-only`, `--cap-drop=ALL`, and no mounts or environment copy.
7. Add tests for argument order, spaces, empty required fields, output routing, inspect decoding, and probe failures.
8. Update `internal/launcher/dockercli/README.md` with the new non-test file and transport-only responsibility.

### Phase 3: Preserve Explicit Image Intent
Purpose: Let launcher policy distinguish a default base reference from an explicit user override.
Status: to be done
Done when: both binaries pass exact `--image` intent without changing command parsing or current image values.

1. Add `ImageOverride bool` to `launchplan.Options`; keep `NoHostMCP` unchanged.
2. In `internal/cli/cli.go`, use `FlagSet.Visit` after parsing to detect whether `image` was explicitly supplied.
3. Pass that bit through the existing `Launcher.Launch` interface and `clitest.RecordingLauncher`.
4. Add CLI tests for omitted image, explicit default-valued image, explicit other image, and arguments after `--`.
5. Update both hand-written command usage strings to explain project Dockerfile auto-selection and override precedence.
6. Update `cmd/codex-safe/main_test.go` and `cmd/agents-safe/main_test.go` to assert the new help text and options.

### Phase 4: Project Image Preparation
Purpose: Confirm, build, cache, and validate the project image only on the new-container path.
Status: to be done
Done when: a missing valid cache key builds once with confirmation, while valid cache, decline, and failure paths are
observable and deterministic.

1. Add `internal/launcher/project_environment.go` for policy and orchestration; keep hashing in `projectenv`.
2. Discover the definition in `DockerLauncher.Launch` unless `ImageOverride` is true, before host-MCP allocation.
3. Store base reference, definition, requested session label, and final image separately on `launchAttempt`.
4. Preserve inspect-first behavior: do not preflight, prompt, build, pull, or probe when a running session is reusable.
5. On the create path, preflight the base reference, resolve its ID, and derive the project cache key and local tag.
6. Accept only a cached image whose full labels and compatibility checks match; otherwise enter the build path.
7. Add a `[y/N]` confirmation using existing terminal streams; only `y` and `yes` build, and all text goes to stderr.
8. Build with fixed context and labels, then re-discover the definition and re-resolve the base ID before use.

### Phase 5: Compatibility and Session Reuse
Purpose: Prevent a derived image or active session from violating immutable startup and environment contracts.
Status: to be done
Done when: only compatible images create sessions, and every active-session combination follows the design matrix.

1. Validate project-image labels, architecture, root user, exact entrypoint, exact `serve` command, and `DOCKER_HOST`.
2. Probe `tini`, `codex-safe-session`, `codex`, and Docker CLI through the immutable image ID with bounded contexts.
3. Set `attempt.image` to the immutable derived ID before session or relay-sidecar request construction.
4. Add `codex-safe.project-environment` to session create labels with `absent`, `override`, or definition digest.
5. Validate this label before user-mount and host-MCP reuse checks on running, waited, and retry paths.
6. Preserve explicit `--image` active-session semantics and map a missing legacy label to the absent case.
7. Add a focused mismatch error naming running and requested values and instructing the user to finish the session.
8. Update create-argv and launcher lifecycle tests for the label, compatibility order, no fallback, and no stdout data.

### Phase 6: Focused Integration and Regression Coverage
Purpose: Prove the complete launcher decision tree with scripted Docker and filesystem fixtures.
Status: to be done
Done when: focused tests cover every contract branch without requiring real Docker or Sysbox.

1. Add launcher tests for absent definition, valid cache, accepted build, declined build, and non-interactive input.
2. Prove build failure, invalid cached image, failed probe, changed context, and moved base reference create no
   container.
3. Prove explicit `--image` performs no `.agents-safe/` validation or project build.
4. Cover the reuse matrix: legacy missing, absent, override, matching digest, changed digest, and removed Dockerfile.
5. Cover linked-worktree root selection and `.agents-safe/` paths containing spaces and shell metacharacters.
6. Prove host-MCP cold create uses the derived ID for both containers and recovery uses the session's inspected ID.
7. Prove two scripted cold callers remain compatible with the existing deterministic create-race handling.
8. Run focused package tests and update package READMEs if implementation file ownership changed.

### Phase 7: Real Docker and Sysbox Proof
Purpose: Demonstrate that a real derived image supplies a tool without weakening the existing isolation contract.
Status: to be done
Done when: a dependency-free fixture image runs through `agents-safe`, caches correctly, and preserves smoke gates.

1. Add `tests/smoke/sysbox_project_environment_test.go` and focused fixture helpers rather than enlarging unrelated
   tests.
2. Create `.agents-safe/Dockerfile` inside the temporary fixture worktree; add a small executable with shell built-ins.
3. Use production launcher orchestration with test-controlled confirmation for the first real build.
4. Run the cached path through the public `bin/agents-safe` binary so CLI discovery and selection are covered.
5. Hold one command active, change the fixture Dockerfile, and assert the finish-active-session diagnostic.
6. After release, build the new definition and prove the new executable behavior and environment label.
7. Re-run existing linked-worktree, nested-Docker, Codex-home, host-MCP, and host-socket isolation assertions.
8. Register cleanup for derived fixture tags, images, containers, worktrees, and temporary files on success or failure.

### Phase 8: Documentation, Full Validation, and Handoff
Purpose: Leave the repository contract accurate and hand completed implementation to review rather than acceptance.
Status: to be done
Done when: docs describe shipped behavior, all required gates pass, and the plan is in `review/` with evidence.

1. Update `README.md` with the `.agents-safe/Dockerfile` UX, confirmation, cache, failure, and `--image` precedence.
2. Update `docs/design-docs/codex-safe.md` with discovery, build boundary, label, and active-reuse integration.
3. Set the project-environments design status to `Implemented` only after real-host validation passes.
4. Add `internal/launcher/projectenv/` to `ARCHITECTURE.md` and keep module paths and ownership accurate.
5. Update `docs/dependencies.md` only if implementation adds an approved dependency; none is expected in this plan.
6. Run all validation gates below and record exact results under `Progress Notes`.
7. Move this file to `docs/exec-plans/review/`, set `Status: in review`, and update `docs/exec-plans/index.md` links.
8. Request design, plan, and implementation review; do not move the plan to `completed/` before acceptance.

## Validation Gates

- `go test ./internal/launcher/projectenv` passes all definition, digest, cache-key, and path-boundary tests.
- `go test ./internal/launcher/dockercli` passes exact build, image-inspect, and confined-probe transport tests.
- `go test ./internal/cli ./cmd/codex-safe ./cmd/agents-safe` proves explicit-image intent and help text.
- `go test ./internal/launcher -run 'ProjectEnvironment|ProjectImage'` passes all preparation and reuse branches.
- `go test ./internal/launcher` passes the existing lifecycle, user-mount, host-MCP, and create-race regressions.
- `rg -n 'type=bind|--mount' internal/launcher/dockercli/build.go` finds no project-image build mount mechanism.
- A test asserts project build context equals `<project-root>/.agents-safe` and never the worktree root.
- A test asserts build/probe output is absent from launcher stdout and present on stderr when emitted.
- A test asserts active-session reuse performs no image inspect, pull, build, or compatibility probe.
- A test asserts build or compatibility failure performs no session or relay-sidecar create and no base fallback.
- `make lint` passes after all changed Go files are formatted with `gofmt`.
- `make test` passes the repository unit and smoke-module static suites.
- `make docker-build` passes and preserves the base image entrypoint, command, and required binaries.
- `make check-docs` reports every Mermaid block and documentation link valid.
- `make test-smoke-go` passes on a compatible Sysbox host, including the new project-environment scenario.
- `git diff --check` reports no whitespace errors.
- `git status --short` contains only intentional implementation, test, doc, and plan changes.

## Risks and Constraints

- A project Dockerfile executes through host Docker before the Sysbox session exists. The fixed context and prompt are
  security boundaries; neither may be widened or removed as a convenience refactor.
- The worktree can change while Docker reads it. Re-discovery after build is required so a stale digest is never
  attached to an image built from different bytes.
- The base reference is currently a local mutable tag. Resolve it before and after build and fail if its ID moves.
- Cached-image presence replaces durable trust state. Label and compatibility checks are required before skipping the
  prompt; tag presence alone is insufficient.
- Compatibility probes execute project-image binaries with host Docker. They must remain mount-free, networkless,
  capability-free, read-only, and bounded.
- Adding a label affects every create-argv fixture and real-host assertion. Update expectations without weakening
  existing Sysbox, mount, host-MCP, or user-state checks.
- Concurrent cold launches may duplicate expensive build work. Do not add a lock or host-state registry in this plan.
- The repository example uses `build-essential`, which has not received separate dependency approval. Do not add the
  example Dockerfile during this plan.

## Out of Scope

- `environment.toml`, Dev Container Features, Nix, `mise`, `asdf`, or ecosystem auto-detection.
- Prebuilt project images from a registry.
- New CLI flags for trust, rebuild, cleanup, or disabling project discovery.
- Startup or post-create hooks.
- BuildKit secret mounts, SSH forwarding, or private-registry credentials.
- Persistent Go, Gradle, Maven, npm, or nested-Docker caches.
- Automatic project-image or BuildKit-cache pruning.
- A host-side build lock or trust database.
- Checking in this repository's example `.agents-safe/Dockerfile` without explicit dependency approval.

## Progress Notes

- Add dated notes during execution for completed phases, validation results, deviations, and blockers.
- Before moving to `review/`, record the final derived-image ID used by the smoke fixture and confirm cleanup left no
  fixture container or image behind.
