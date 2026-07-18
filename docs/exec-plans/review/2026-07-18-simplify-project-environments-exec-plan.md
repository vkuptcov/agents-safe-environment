# Exec Plan: Simplify Project Environments

- Status: in review
- Created: 2026-07-18
- Design: [`docs/design-docs/project-environments.md`](../../design-docs/project-environments.md)
- Scope:
  - `internal/launcher/projectenv/`
  - `internal/launcher/` and `internal/launcher/dockercli/`
  - project-environment tests and owning documentation

## Objective

Reduce the project-environment implementation to the minimum state needed for automatic builds. Let Docker/BuildKit
own cache invalidation, and treat `.agents-safe/Dockerfile` itself as consent to build on the create path.

## Done Criteria

- Project images use one stable per-project tag and run `docker build` whenever a new session is created.
- The launcher computes no project-context digest and stores no cache identity labels.
- An active session is reused until it exits, even if `.agents-safe/` changes or is removed.
- Builds require no launcher prompt; Dockerfile presence is the explicit project opt-in.
- Required launcher, lint, Docker-build, and documentation gates pass; a missing Sysbox runtime is reported explicitly.

## Current Baseline

The implementation computes a second hash from the definition and base image, stores four image labels, inspects the
full derived-image configuration, and starts an extra confined container to probe inherited binaries. Those layers add
transport types and branches even though the project image is already built from an approved base and any broken
startup contract fails closed when Docker starts the session.

## Implementation Decisions

- Use one stable tag derived from the project key; Docker/BuildKit owns cache invalidation.
- Keep immutable image IDs for session and relay creation after build validation.
- Do not label a build with a launcher-computed digest or attempt to duplicate Docker's context snapshot.
- Let normal session startup validate inherited runtime binaries instead of maintaining a second execution path.

## Phases

### Phase 1: Identity and Transport Cleanup
Purpose: Remove redundant cache identity and compatibility-probe transport.
Status: done
Done when: image naming and inspection expose only the data used by cache selection.

1. Simplify context identity and image-tag helpers.
2. Remove probe-only request and argv types; keep only static image-config inspection.
3. Update focused transport and definition tests.

### Phase 2: Launcher Simplification
Purpose: Make image preparation a linear cache-or-build flow.
Status: done
Done when: the launcher preserves immutable creation and fail-closed build/configuration errors with fewer branches.

1. Replace compatibility orchestration with static configuration validation.
2. Update launcher and real-host fixture tests.
3. Update the implementation review with responses.

### Phase 3: Documentation and Validation
Purpose: Align the durable contract and prove the simplified implementation.
Status: done
Done when: owning docs describe the shipped behavior, applicable gates are green, and environment blockers are recorded.

1. Update the design doc, package maps, and original feature plan.
2. Run formatting, focused tests, repository gates, and real Sysbox smoke.
3. Move this plan to `review/` and update the plan index.

### Phase 4: BuildKit-Owned Cache
Purpose: Remove launcher-owned content identity and confirmation.
Status: done
Done when: every cold create builds the stable project tag and active sessions retain ordinary image semantics.

1. Reduce project discovery to fixed context and Dockerfile validation.
2. Remove digest labels, active-session digest checks, prompts, and pre-build cache inspection.
3. Exercise production build orchestration in unit and real-host smoke tests.
4. Align the design, README, original feature plan, and implementation review.

## Validation Gates

- `go test ./internal/launcher/projectenv ./internal/launcher/dockercli ./internal/launcher` passes.
- `make lint` passes.
- `make test` passes.
- `make docker-build` passes.
- `make check-docs` passes.
- `make test-smoke-go` passes on the compatible host.
- `git diff --check` passes.

## Risks and Constraints

- A project Dockerfile can still break the inherited base-image startup contract; Docker session startup must fail
  without falling back to the base image.
- Keep the build context fixed at `.agents-safe/` and preserve explicit `--image` precedence.

## Out of Scope

- New manifests, hooks, secrets, persistent volumes, or package-manager caches.
- Changing the shared runtime image or adding dependencies.
- Automatic cleanup of old derived images.

## Progress Notes

- 2026-07-18: Branch review identified the redundant cache-key and compatibility-probe layers as the primary
  simplification targets.
- 2026-07-18: Removed the second cache-key hash, two redundant image labels, and the separate compatibility
  container. Added an explicit cached-image unit path and kept static startup-config validation.
- 2026-07-18: `go test ./internal/launcher/projectenv ./internal/launcher/dockercli ./internal/launcher`,
  `make test`, `make lint`, `make docker-build`, `make check-docs`, and `git diff --check` passed.
- 2026-07-18: `make test-smoke-go` could not exercise Sysbox because the current Docker daemon registers only
  `crun`, `runc`, and `io.containerd.runc.v2`. The suite failed before changed runtime code with
  `Docker runtime "sysbox-runc" is not registered`; cleanup left no managed container or fixture image.
- 2026-07-18: The owner selected Dockerfile-as-consent semantics. Phase 4 reopens implementation to remove the
  remaining launcher digest and delegate cache decisions entirely to Docker/BuildKit.
- 2026-07-18: Removed context hashing, digest/cache labels, the build prompt, pre-build image-cache inspection, and
  active-session definition checks. Discovery now runs only after active-session reuse has been ruled out.
- 2026-07-18: `go test ./internal/launcher/projectenv ./internal/launcher/dockercli ./internal/launcher`, `make test`,
  `make lint`, `make docker-build`, `make check-docs`, and `git diff --check` pass for the final contract.
- 2026-07-18: `make test-smoke-go` is blocked before changed runtime code because this Docker daemon does not register
  `sysbox-runc`; the host also has no `XDG_RUNTIME_DIR` for the host-MCP scenario. Cleanup left no managed container
  or `codex-safe-project-*` fixture image.
