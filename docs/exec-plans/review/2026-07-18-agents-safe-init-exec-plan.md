# Exec Plan: agents-safe Project Initialization

- Status: in review
- Created: 2026-07-18
- Design: [`docs/design-docs/project-environments.md`](../../design-docs/project-environments.md)
- Scope:
  - `cmd/agents-safe/`
  - `internal/launcher/projectenv/`
  - project-environment user documentation

## Objective

Add a host-side `agents-safe init` command that prepares an inactive project-environment sample without requiring
Docker or changing the environment used by the next launch.

## Done Criteria

- `agents-safe init` creates `.agents-safe/Dockerfile.sample` at the selected Git worktree root.
- The generated sample inherits from `AGENTS_SAFE_BASE` and shows how to copy a pre-build toolchain stage.
- Initialization never overwrites an existing sample or accepts a symlink as the `.agents-safe` directory.
- `agents-safe -- init` still executes `init` as a container command.
- Focused tests, repository-wide Go gates, and documentation checks pass.

## Current Baseline

The launcher discovers an active `.agents-safe/Dockerfile`, but users must create that directory and definition by
hand. `agents-safe` treats every non-option argument as a container command and initializes Docker before dispatch.

## Implementation Decisions

- Keep `Dockerfile.sample` inactive: only renaming it to `Dockerfile` opts the project into host-side builds.
- Resolve the target through existing Git project discovery and write at the worktree root, even when invoked below it.
- Reserve only a leading `init` as the host subcommand; an explicit `--` preserves access to a container command named
  `init`.
- Fail rather than overwrite an existing sample.

## Phases

### Phase 1: Initialization Contract
Purpose: Define safe filesystem and command-dispatch behavior.
Status: done
Done when: the design and plan state the target, activation boundary, overwrite rule, and command escape hatch.

1. Update the project-environment design contract.
2. Document the public command shape and activation step.

### Phase 2: Host Command
Purpose: Create the sample before any container-launch dependencies are initialized.
Status: done
Done when: `agents-safe init` creates the sample at the discovered worktree root without contacting Docker.

1. Add sample creation to `internal/launcher/projectenv/`.
2. Dispatch the leading `init` subcommand in `cmd/agents-safe/`.
3. Preserve `agents-safe -- init` as the generic launcher path.

### Phase 3: Verification
Purpose: Prove safe creation, failure behavior, and unchanged generic command dispatch.
Status: done
Done when: focused and repository-wide gates pass and the plan is ready for owner review.

1. Add filesystem and command-dispatch tests.
2. Run formatting, focused tests, `make lint`, `make test`, and `make check-docs`.
3. Move this plan to `review/` and update the plan index.

## Validation Gates

- `go test ./internal/launcher/projectenv ./cmd/agents-safe` passes.
- `make lint` passes.
- `make test` passes.
- `make check-docs` passes.
- A focused CLI test proves the init path does not construct a Docker launcher.

## Risks and Constraints

- The generated file must remain a valid Dockerfile after it is renamed; commented multi-line examples must comment
  every physical line.
- Existing user content in `.agents-safe/` must be preserved.

## Out of Scope

- Automatically activating the sample as `.agents-safe/Dockerfile`.
- Inferring project toolchains or modifying an existing project Dockerfile.
- Adding dependencies or contacting Docker during initialization.

## Progress Notes

- 2026-07-18: Plan created; implementation started.
- 2026-07-18: Added safe sample creation, pre-Docker CLI dispatch, focused tests, and durable command documentation.
- 2026-07-18: Focused tests, `make lint`, `make test`, and `make check-docs` passed; moved the plan to review.
- 2026-07-18: Moved the sample body into an embedded `Dockerfile.sample` resource to keep template text out of Go
  source while preserving a self-contained binary.
