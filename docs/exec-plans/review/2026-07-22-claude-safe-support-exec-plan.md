# Exec Plan: claude-safe Support

- Status: review
- Created: 2026-07-22
- Design: `docs/design-docs/claude-safe.md`
- Scope:
  - `cmd/claude-safe/`, shared CLI/config resolution, and launcher state/mount policy
  - Claude Code installation/update volume and container image integration
  - host-MCP discovery, unit/smoke tests, and owning documentation

## Objective

Add a `claude-safe` product launcher with the same project discovery, isolation, persistent state, update, and
session-lifecycle behavior as `codex-safe`. Codex and Claude Code must be able to run concurrently in the same managed
container so they share the worktree and private Docker daemon without sharing product credentials or history.

## Done Criteria

- `claude-safe` starts only the volume-backed Claude Code executable and forwards invocation arguments without a shell.
- Host Claude Code state is available through explicit validated mounts and remains separate from Codex state.
- `claude-safe update` refreshes a daemon-local Claude Code volume without Git discovery or Sysbox.
- Every managed session mounts both Codex and Claude Code installations read-only.
- `codex-safe`, `claude-safe`, and `agents-safe` resolve one compatible creation fingerprint for the same worktree.
- Host loopback MCP endpoints configured for either product are forwarded through the same confined relay contract.
- Focused, repository-wide, Docker, documentation, and real-host smoke gates pass or have an explicit environment
  blocker.

## Current Baseline

`codex-safe` and `agents-safe` already share one deterministic project container and one foreground-command lifecycle.
The container always receives a read-only Codex installation volume, while Codex state is an optional host bind.
Project configuration has `[codex]` command arguments but no Claude-specific state, arguments, executable volume, or
update path. Host-MCP preflight reads only Codex `config.toml`.

## Implementation Decisions

- Shared session identity: all public launchers resolve both products' creation-time mounts and installation volumes.
- Separate state: Codex and Claude Code keep independent host state; only the project and container runtime are shared.
- Claude state layout: keep native default paths as separate `~/.claude` and `~/.claude.json` mounts without setting
  `CLAUDE_CONFIG_DIR`; when the host explicitly sets that variable, mount its one directory and preserve the override.
- Product permission default: run Claude Code with `--permission-mode auto` while preserving an explicit invocation
  permission mode.
- Managed updates: use Anthropic's official native installer in a maintenance container and disable Claude Code's
  background updater in read-only project sessions.
- MCP compatibility: union trusted user/local loopback endpoints from Codex and Claude host configuration before
  fingerprinting; do not infer forwarding from repository-controlled `.mcp.json`.

## Phases

### Phase 1: Durable Contract
Purpose: Define the multi-product session, state, update, and interaction boundaries before runtime changes.
Status: done
Done when: the design docs and plan describe one unambiguous shared-container contract for both launchers.

1. Add the `claude-safe` design document.
2. Update architecture and owning design-doc catalogs.
3. Record the implementation phases and validation gates here.

### Phase 2: Shared State and MCP Resolution
Purpose: Make every launcher resolve the same validated Codex-plus-Claude creation plan.
Status: done
Done when: Claude state is mounted and routed independently, and Codex/Claude host MCP endpoints form one stable set.

1. Extend typed project config with Claude arguments and mount roles.
2. Resolve default and explicit Claude config directories without Docker access.
3. Route `CLAUDE_CONFIG_DIR` through command execution and add diagnostic labels/warnings.
4. Parse trusted Claude user/local MCP configuration and merge it with Codex discovery.
5. Bump the creation fingerprint schema and add focused tests.

### Phase 3: Product CLI and Installation
Purpose: Provide the public Claude Code command and portable managed update path.
Status: done
Done when: `claude-safe` launches the volume binary and `claude-safe update` refreshes it independently of Sysbox.

1. Add Claude command assembly and explicit permission-mode precedence.
2. Add `cmd/claude-safe` with launch/update dispatch and tests.
3. Add the official-installer wrapper and read-only session volume.
4. Build/install all three host launchers and initialize both product volumes.
5. Disable Claude Code background self-update inside project sessions.

### Phase 4: Integration Coverage and Documentation
Purpose: Prove both products coexist in one session contract and leave maintainers an accurate operating model.
Status: done
Done when: tests cover both products and all affected docs describe the implemented behavior.

1. Extend request, config, CLI, update, and host-MCP tests.
2. Extend real-host smoke coverage for the Claude binary/state and shared-session reuse.
3. Update README, testing, security, dependency, Makefile, architecture, and design documentation.
4. Move this plan to `review/` with final progress notes after all in-scope checks pass.

## Validation Gates

- `gofmt` changes no already-formatted Go source after the final formatting pass.
- `go test ./cmd/claude-safe ./internal/cli ./internal/launchcli ./internal/launcher/...` passes.
- `go vet ./cmd/claude-safe ./internal/cli ./internal/launchcli ./internal/launcher/...` passes.
- `bash -n container/claude-safe-update` and `sh -n container/codex-safe-update` pass.
- `make test` passes.
- `make lint` passes.
- `make docker-build` builds the image and initializes both daemon-local installation volumes.
- A disposable ordinary-Docker volume proves the Claude updater creates a usable absolute executable and sessions
  cannot write the read-only mount.
- `make check-docs` passes.
- `make test-smoke-go` proves real Sysbox state, executable, and same-container behavior for both product launchers.

## Risks and Constraints

- Existing `.agents-safe/config.toml` files replace the full mount list. New optional Claude roles remain omitted until
  the owner adds them or regenerates the local snapshot; the launcher must warn instead of silently widening access.
- Claude Code's native installer and release service are external trusted code, equivalent to the existing Codex
  installer boundary.
- Concurrent agents can race on shared worktree files and nested Docker resources. The launcher provides coexistence,
  not application-level locking or a direct chat protocol.
- Claude Code background-agent processes outliving the foreground CLI are not a new lifecycle class; the managed
  container still follows registered foreground wrappers.

## Out of Scope

- A direct Codex-to-Claude messaging protocol or shared conversation history.
- Automatic mutation or migration of existing ignored `.agents-safe/config.toml` files.
- Repository-controlled `.mcp.json` host-loopback forwarding.
- Windows/macOS project-session backends, version pinning UI, rollback, or automatic background updates.

## Progress Notes

- 2026-07-22: Baseline traced through the shared CLI, creation fingerprint, mount normalization, update volume, and
  host-MCP relay. Anthropic's current native install/state paths were verified from official docs and a disposable
  local `CLAUDE_CONFIG_DIR` probe.
- 2026-07-22: Implemented the product CLI, independent read-only installation volume and updater, native/explicit
  state routing, typed Claude arguments, fingerprint schema 4, and the Codex-plus-Claude host-MCP endpoint union.
- 2026-07-22: Added focused unit coverage and a real-host smoke scenario that holds one session while both product
  launchers execute and Claude persists global configuration through the host mount. `make test` passes.
- 2026-07-22: `make lint`, `make test`, `make docker-build`, `make check-docs`, and the full
  `make test-smoke-go` gate pass. A disposable ordinary-Docker volume installed Claude Code 2.1.217, ran the managed
  binary from a read-only mount, and rejected a UID-0 write before cleanup.
