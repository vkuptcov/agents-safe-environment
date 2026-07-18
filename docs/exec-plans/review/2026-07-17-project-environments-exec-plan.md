# Exec Plan: Project-Specific Agent Environments

- Status: in review
- Created: 2026-07-17
- Design: [`docs/design-docs/project-environments.md`](../../design-docs/project-environments.md)
- Scope:
  - `internal/cli/`, `cmd/codex-safe/`, `cmd/agents-safe/`, and `internal/testutil/clitest/`
  - `internal/launcher/`, `internal/launcher/projectenv/`, and `internal/launcher/dockercli/`
  - `tests/smoke/`
  - `README.md`, `ARCHITECTURE.md`, and owning documentation

## Objective

Let a Git project add system toolchains through `.agents-safe/Dockerfile` while preserving the existing launcher,
Sysbox, mount, host-MCP, and session-lifecycle boundaries. The Dockerfile itself opts the project into automatic
host-side builds. Docker/BuildKit owns incremental cache decisions.

## Done Criteria

- Both public launchers automatically use `.agents-safe/Dockerfile` when `--image` was not explicit.
- A missing project Dockerfile preserves the base-image behavior and call order.
- Build context is fixed at `.agents-safe/`, and build output stays on the diagnostic stream.
- Every cold session creation invokes `docker build` for one stable per-project tag.
- The launcher computes no parallel context digest and stores no project-cache labels.
- Derived images are checked against the base image's static startup contract before session creation.
- Session creation uses the derived image's immutable ID, not the mutable local tag.
- An active session remains reusable until it exits, even if the project Dockerfile changes or is removed.
- Build and compatibility failures create no session and use no fallback image.
- Focused unit tests and a dependency-free real-Sysbox fixture prove the behavior.
- Required documentation and package maps describe the implemented contract.
- Required repository gates pass, or an environment-specific Sysbox blocker is recorded.

## Implementation Decisions

- **One project contract:** version 1 recognizes only `.agents-safe/Dockerfile`; no manifest or hooks.
- **Dockerfile as consent:** its presence authorizes the automatic host-side build; there is no second prompt or
  trust database.
- **Fixed build context:** pass the `.agents-safe/` directory to Docker and let Docker apply its context and
  `.dockerignore` semantics.
- **Explicit-image precedence:** an explicit `--image` bypasses project-environment discovery and builds.
- **Stable local tag:** use `codex-safe-project-<project-key>:local`; BuildKit decides whether layers are reusable.
- **No launcher digest:** do not traverse or hash the context and do not attach cache-identity labels.
- **Immutable create input:** inspect the build result and create the session and host-MCP sidecar from its immutable
  image ID.
- **Static compatibility check:** validate architecture, root user, entrypoint, command, and `DOCKER_HOST`; normal
  session startup is the authoritative execution check.
- **Ordinary active-session lifecycle:** inspect and reuse an existing session before project discovery; project
  changes take effect on the next cold creation.
- **No build lock in version 1:** concurrent cold callers may duplicate build work; deterministic container creation
  remains the correctness lock.
- **No new dependency:** use the standard library and existing Docker CLI transport.

## Phases

### Phase 1: Project Discovery and Image Naming
Status: done

1. Add `internal/launcher/projectenv/` and its package map.
2. Discover the fixed `.agents-safe/` context and require a real directory plus regular Dockerfile.
3. Derive one stable local image tag from the existing project key.
4. Cover absent, valid, and symlink-invalid definitions plus stable image naming.

### Phase 2: Typed Docker Image Operations
Status: done

1. Add typed build and image-inspection requests to `dockercli`.
2. Build with the Dockerfile, stable tag, `AGENTS_SAFE_BASE` argument, and fixed context.
3. Route build output to diagnostics and retain bounded failure text.
4. Decode the immutable image ID and static startup configuration.

### Phase 3: Preserve Explicit Image Intent
Status: done

1. Carry explicit `--image` intent through the launch plan.
2. Keep an explicitly supplied default-valued image distinct from an omitted flag.
3. Cover both public binaries and CLI parsing behavior.

### Phase 4: Automatic Project Image Preparation
Status: done

1. Preserve inspect-first active-session reuse.
2. On a cold create, preflight the base image and invoke `docker build` unconditionally.
3. Inspect and validate the stable tag after the build.
4. Pin the immutable derived ID for session and relay construction.
5. Fail closed without falling back to the base image.

### Phase 5: Lifecycle and Regression Coverage
Status: done

1. Cover automatic cold build, immutable create input, and explicit-image bypass.
2. Prove a running session is reused without project discovery or rebuild.
3. Prove a changed Dockerfile affects the next cold creation through the stable tag.
4. Preserve linked-worktree, paths-with-spaces, host-MCP, create-race, and cleanup coverage.

### Phase 6: Documentation, Validation, and Handoff
Status: done

1. Align the design, README, architecture map, smoke guide, and review artifact.
2. Run formatting, focused tests, repository gates, and the real Sysbox gate.
3. Record exact results and leave this plan in `review/` pending owner acceptance.

## Validation Gates

- `go test ./internal/launcher/projectenv ./internal/launcher/dockercli ./internal/launcher`
- `make test`
- `make lint`
- `make docker-build`
- `make check-docs`
- `make test-smoke-go` on a compatible Sysbox host
- `git diff --check`

## Risks and Constraints

- A project Dockerfile executes through host Docker before the Sysbox session exists. Keeping the context fixed at
  `.agents-safe/` limits the directly supplied build context, but Dockerfile instructions remain project-controlled.
- A stable mutable tag is not used to create the session. The inspected immutable image ID prevents a later tag move
  from changing the prepared launch.
- Docker/BuildKit cache policy, base-image freshness, and `.dockerignore` behavior are deliberately Docker-owned.
- Project changes do not replace an active session. Users finish that session before expecting a new image.
- No project image or BuildKit-cache cleanup policy is added in version 1.

## Out of Scope

- Manifests, hooks, secrets, persistent volumes, or package-manager cache mounts.
- A host-side build lock, trust database, or launcher-owned cache index.
- Automatic cleanup of derived images or BuildKit cache.
- New system packages in this repository's own `.agents-safe/Dockerfile` without dependency approval.

## Progress Notes

- 2026-07-17: Implemented the original digest-and-confirmation design and validated it on a compatible Sysbox host.
- 2026-07-18: Review removed a redundant second cache-key hash, duplicate identity labels, and a separate
  compatibility container.
- 2026-07-18: The owner selected Dockerfile-as-consent and Docker/BuildKit-owned caching. The launcher-owned context
  digest, build prompt, cache labels, pre-build cache inspection, and active-session definition checks were removed.
- 2026-07-18: Focused tests, `make test`, `make lint`, `make docker-build`, `make check-docs`, and `git diff --check`
  pass for the revised contract. Real-host smoke is blocked before the changed path because `sysbox-runc` is not
  registered; cleanup left no managed container or project fixture image.
