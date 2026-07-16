# Exec Plan: Moby Engine Client Migration

- Status: cancelled
- Created: 2026-07-16
- Design: [`codex-safe.md`](../../design-docs/codex-safe.md)
- Scope:
  - `internal/launcher/`
  - root Go module dependencies
  - launcher design and package documentation

## Objective

Replace host Docker CLI subprocess orchestration with the supported Moby Engine API client so container
configuration, inspection, execution, and errors use typed contracts while preserving launcher lifecycle behavior.

## Done Criteria

- The launcher uses `github.com/moby/moby/client` and `github.com/moby/moby/api`.
- No production launcher code invokes the host `docker` executable.
- Container creation preserves the current Sysbox, mount, identity, label, and auto-remove contract.
- Interactive and non-interactive exec preserve stdin, stdout, stderr, TTY, and child exit status.
- Deterministic reuse, concurrent-create handling, and stopped-container retry behavior remain covered by tests.
- Required Go and documentation checks pass.

## Current Baseline

`internal/launcher/docker.go` builds Docker CLI argument vectors, executes subprocesses, parses inspect JSON and
diagnostic text, and also owns the project-container lifecycle state machine.

## Implementation Decisions

- Use the supported split Moby modules; do not import the deprecated `github.com/docker/docker` root module.
- Keep lifecycle policy in the launcher and place Engine API transport behind a narrow testable interface.
- Use typed Docker error classification instead of matching CLI stderr.
- Preserve Docker environment-based endpoint selection and API version negotiation.

## Phases

### Phase 1: Engine Boundary
Purpose: Introduce the approved dependencies and a focused Docker Engine abstraction.
Status: done
Done when: production construction opens a negotiated Engine API client and tests can inject a fake.

1. Add the Moby client and API modules.
2. Define the minimal Engine operations required by the launcher.
3. Wire client construction and close ownership.

### Phase 2: Typed Container Lifecycle
Purpose: Replace CLI create, inspect, and preflight calls with typed Engine operations.
Status: done
Done when: Sysbox container configuration and reuse checks no longer depend on CLI arguments or JSON parsing.

1. Build typed container, host, mount, and label configuration.
2. Use daemon info and image inspect for preflight.
3. Classify not-found and name-conflict errors through Docker error definitions.

### Phase 3: Attached Exec
Purpose: Preserve command I/O and exit semantics through Engine exec APIs.
Status: done
Done when: TTY and non-TTY commands attach correctly and return the wrapped command's exit status.

1. Create and attach the exec instance.
2. Forward stdin and copy raw or multiplexed output as appropriate.
3. Inspect the exec result and translate nonzero status into the launcher exit-error contract.
4. Preserve retry classification for stopped, missing, restarting, and manager-registration failures.

### Phase 4: Cleanup and Validation
Purpose: Remove the subprocess transport and prove the migration preserves behavior.
Status: done
Done when: obsolete CLI helpers are gone, documentation is current, and all required gates pass.

1. Replace CLI-oriented tests with typed configuration and Engine-fake tests.
2. Split launcher files by responsibility and update the package README and design doc.
3. Run focused, repository-wide, and documentation gates.
4. Move this plan and the naming plan to `review/`.

## Validation Gates

- `go test ./internal/launcher ./internal/cli ./cmd/codex-safe ./cmd/agents-safe` passes.
- `GOCACHE=/tmp/agents-safe-go-build make test` passes.
- `make check-docs` passes.
- `git diff --check` passes.
- `rg -n "os/exec|exec\\.Command|DockerCommandRunner|BuildDockerRunArgs|BuildDockerExecArgs" internal/launcher`
  returns no production transport matches.

## Risks and Constraints

- Docker exec uses a hijacked connection; TTY and non-TTY streams require different copy behavior.
- Client construction must not silently change the supported daemon endpoint contract.
- Moby API types may change independently; dependencies must stay pinned and reviewed.
- Preserve the existing uncommitted naming and documentation refactor.

## Out of Scope

- Changing deterministic container identity or reuse policy.
- Pulling missing images automatically.
- Adding non-Linux or remote-Docker support beyond the client's existing environment contract.
- Changing the nested Docker daemon inside the Sysbox container.

## Progress Notes

- 2026-07-16: Owner approved migration to the supported Moby Engine client and API modules.
- 2026-07-16: Added pinned Moby client/API and containerd error modules to the root and smoke Go modules.
- 2026-07-16: Replaced Docker CLI subprocesses with typed create, inspect, start, attach, resize, and exec operations.
- 2026-07-16: Split launcher configuration, lifecycle, Engine transport, exec policy, and host validation by role.
- 2026-07-16: `make test`, `make check-docs`, focused tests, smoke compilation, vet, and diff checks passed.
- 2026-07-16: An isolated real Sysbox scenario passed. The complete real-host suite is blocked by intermittent host
  Sysbox cgroup teardown failures, including a deleted `init.scope` before the next exec; this occurs before the
  requested command starts.
- 2026-07-16: Owner cancelled the migration after review. The launcher returned to the host Docker CLI so attach,
  terminal, stream, and exit-status behavior remain owned by the Docker CLI rather than project code.
- 2026-07-16: The complete real Sysbox smoke suite passed after the CLI transport was restored.
- 2026-07-16: Owner accepted the terminal cancelled state and archived the plan.
