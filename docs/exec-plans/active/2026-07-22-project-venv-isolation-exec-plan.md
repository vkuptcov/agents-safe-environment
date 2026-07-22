# Exec Plan: Project Virtual Environment Isolation

- Status: active
- Created: 2026-07-22
- Design: `docs/design-docs/agents-safe.md`
- Scope:
  - `internal/launcher/dockercli/`
  - `internal/launcher/`
  - `internal/cli/`
  - `docs/design-docs/agents-safe.md`
  - `docs/design-docs/project-launcher-configuration.md`
  - `tests/smoke/`

## Objective

Keep host Python virtual environments out of managed sessions so image-specific environments cannot read or modify
them.

## Done Criteria

- With `use_host_python_venv = false`, each session masks the configured root `.venv` and every discovered
  project-local Python virtual environment with a writable container-local filesystem.
- `use_host_python_venv = true` and its explicit CLI override expose host environments without tmpfs mounts.
- An existing session without that mask is not reused after the change.
- Focused launcher tests, the repository test gate, documentation check, and the real mount proof pass.

## Current Baseline

The worktree bind is writable and includes `.venv`; a command in a project image can therefore mutate the host
environment. The existing uv cache bind intentionally remains shared and is not a virtual environment.

## Implementation Decisions

- Use a session-local `tmpfs` mounted after the project bind; no host virtual environment is copied or retained.
- Discover existing environments by regular `pyvenv.cfg` markers on every launch; do not follow symlinks or scan Git
  metadata.
- Keep tmpfs mounts distinct from host bind mounts and expose their base list as `common.tmpfs_mounts`; they have no
  source or logical host-access role.
- Make `use_host_python_venv` a creation-time scalar with safe default `false` and an explicit CLI override.
- Bump the creation-fingerprint schema because the new mount is required for safe session reuse.

## Phases

### Phase 1: Mount Contract
Purpose: Represent and attach the container-local virtual-environment mount.
Status: completed
Done when: every safe-default session masks the complete discovered environment set, while the opt-in host policy
adds no tmpfs mounts.

1. Extend the typed Docker request and argv encoding for `tmpfs` mounts.
2. Discover marked environments and add their masks to managed session creation.
3. Add the config/CLI policy and cover both boolean directions.
4. Version the creation fingerprint to include the policy and discovered targets.

### Phase 2: Durable Contract and Proof
Purpose: Document and exercise the host-environment boundary.
Status: in progress
Done when: documentation distinguishes virtual-environment isolation from the reusable uv cache and a real Sysbox
test proves host content is unchanged.

1. Update the safe-environment mount contract.
2. Add a smoke test that observes independent host and container `.venv` content.

## Validation Gates

- `gofmt -w` on changed Go files completes without changes afterwards.
- `go test ./internal/launcher ./internal/launcher/dockercli` passes.
- `make test` passes.
- `make check-docs` passes.
- `make test-smoke-go` passes on a compatible Linux/Sysbox host and proves `.venv` isolation.

## Risks and Constraints

- `tmpfs` content is intentionally lost when the managed container exits; dependency downloads continue to use the
  separately configured uv cache.
- A running session created before this change must finish before the new mount contract can take effect.

## Out of Scope

- Persisting container virtual environments across session removal.

## Progress Notes

- 2026-07-22: The initial explicit-list design was superseded by owner direction: launch-time marker discovery with
  a `use_host_python_venv` opt-in now owns the contract; the revised implementation was revalidated below.
- 2026-07-22: `common.tmpfs_mounts` now records the conventional root `.venv`; discovery augments that base at launch
  time, while `use_host_python_venv = true` disables the complete venv tmpfs plan.
- 2026-07-22: Focused launcher tests, `make test`, `make lint`, and `make check-docs` pass for the resolved config,
  discovery, fingerprint, and Docker argv changes.
- 2026-07-22: `make test-smoke-go` could not reach the Sysbox tests in this execution environment. The required cold
  Docker build spent about ten minutes downloading its 151 MB Ubuntu package set from slow mirrors and was stopped;
  no test failure occurred. Run that gate where the base image build can complete.
