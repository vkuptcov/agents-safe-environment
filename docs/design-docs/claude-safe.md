# Safe Claude Code Integration

Status: Implemented

Scope:

- the `claude-safe` host command and Claude Code argv policy;
- host Claude Code state exposed to managed sessions;
- the Docker-managed Claude Code installation and update path;
- coexistence with Codex in one managed project container.

The common Sysbox boundary, project/Git mounts, identity mapping, nested Docker daemon, and foreground-command
lifecycle remain owned by [Safe Environment](agents-safe.md) and [Go Session Manager](go-session-manager.md). Typed
project configuration remains owned by [Project Launcher Configuration](project-launcher-configuration.md).

## Purpose and Intent

### Problem

Before this integration, the managed environment could start Codex or arbitrary commands but had no durable Claude
Code binary, host Claude state, or product launcher. Starting Claude through `agents-safe` required an ad hoc install
and could not guarantee that a simultaneous Codex process joined the same creation contract.

### Worked Example

Before:

```text
terminal A: codex-safe       -> managed project container
terminal B: agents-safe ... -> same container, but no managed Claude installation or state
```

After:

```text
terminal A: codex-safe  -> one managed project container -> shared worktree and private dockerd
terminal B: claude-safe -> the same container            -> separate Claude state and process
```

Both agents observe each other's file changes and can use the same nested Docker resources. Their credentials,
configuration, transcripts, and executable installations remain product-specific.

### Chosen Shape

`claude-safe` is a thin product launcher over the same discovery, config resolution, Docker lifecycle, and session
wrapper as `codex-safe`. Every cold session receives both executable volumes and both resolved state mount sets, so
the command used to create the container does not determine whether the other product can join it later.

Claude Code is installed through Anthropic's native installer into a daemon-local named volume. Project containers
mount that volume read-only. A separate ordinary maintenance container is the only supported writer.

### Success Criteria

- Either product can create the project container and the other can join while it is running.
- Claude state persists on the host without being mixed into Codex state or the executable volume.
- Product updates do not require a project, Git discovery, or Sysbox.
- A project session cannot modify either managed executable installation.
- Host MCP forwarding remains one explicit, fingerprinted session capability for both products.

### Tradeoff

Two agents sharing a worktree can conflict at the application layer. This design deliberately exposes shared files,
process-visible project state, and nested Docker resources; it does not serialize edits or introduce a conversation
bridge between the products.

## Contract

### 1. Public Command

`claude-safe [--project PATH] [--image REF] [--no-host-mcp] [--use-host-python-venv] [--force-exec]
[-- CLAUDE ARG...]` always executes the managed Claude Code binary. Arguments after `--` are Claude Code arguments,
never an arbitrary executable and never shell text. The common launcher owns `--force-exec` semantics; it does not
change Claude argv.

The configured default is `--permission-mode auto`. An invocation that explicitly supplies
`--permission-mode`, `--dangerously-skip-permissions`, or `--allow-dangerously-skip-permissions` suppresses only that
configured default; all other configured and invocation arguments retain their order.

`claude-safe update` is dispatched before Git discovery. It needs the host Docker daemon and default image but does not
need a project or Sysbox. `claude-safe -- update` remains a normal in-session Claude invocation.

### 2. Shared Session and Interaction

Container identity remains the deterministic project/UID identity owned by the common launcher. `codex-safe`,
`claude-safe`, and `agents-safe` resolve the same creation-time contract:

- the same normalized project, Git, state, and configured mounts;
- both product executable volumes;
- the same host-MCP endpoint union;
- the same project image, cache routing, identity, and manager protocol.

The first foreground command creates the container. Later commands register through the same session manager and may
overlap. The container shuts down only after the last registered foreground command exits and the idle timeout elapses.

Interaction is through resources the processes intentionally share: the writable worktree, Git metadata, ordinary
container processes, and the private Docker daemon. Product homes and conversation histories are not merged, and the
launcher provides no direct agent-to-agent message protocol.

### 3. Claude State

Claude Code state is optional host state, separate from the installation volume:

- default directory: `~/.claude`;
- default global config: `~/.claude.json`;
- explicit directory: `CLAUDE_CONFIG_DIR`, which stores the global `.claude.json` inside that directory.

The launcher canonicalizes existing sources without contacting Docker. An explicit missing, broken, non-directory, or
unsafe `CLAUDE_CONFIG_DIR` is an error. Absent default state is degradable: `claude-safe` warns and uses ephemeral
container state.

Default state is considered complete only when both native paths already exist. The launcher then mounts `~/.claude`
at `<host-home>/.claude` and `~/.claude.json` at `<host-home>/.claude.json`, without setting
`CLAUDE_CONFIG_DIR`; Claude therefore retains its native split-path behavior. A partial default is treated as absent so
the session cannot persist only half of the product state.

When the host explicitly sets `CLAUDE_CONFIG_DIR`, the launcher mounts that canonical directory at
`<host-home>/.claude` and sets the same variable for every managed command. The directory contains its own
`.claude.json`, so no separate global-file mount is added.

The mounts are read-write because settings, credentials, trust decisions, transcripts, plugins, caches, and history
are mutable product state. They expose those credentials and records to every process in the managed session, just as
the Codex-home mount does for Codex state.

Existing local project config remains authoritative. A full `common.mounts` snapshot that omits the new optional Claude
roles is not silently widened; it produces a degradation warning and Claude runs with ephemeral state until the owner
updates that ignored host-specific file.

### 4. Managed Installation and Updates

The Docker named volume `agents-safe-claude` is mounted at `/opt/agents-safe/claude`. The managed executable path is:

```text
/opt/agents-safe/claude/home/.local/bin/claude
```

`make docker-build` and `claude-safe update` run `/usr/local/bin/claude-safe-update` in an ordinary attached container
with that volume read-write. The wrapper sets its installation-only `HOME` below the volume and runs Anthropic's
official native installer from `https://claude.ai/install.sh`. The installer selects the container's Linux
architecture and verifies the downloaded release checksum.

Every project session mounts the volume read-only. `DISABLE_AUTOUPDATER=1` prevents Claude Code from attempting its
background writer path; explicit `claude update` inside the session is not the supported update path and fails against
the read-only installation. No Claude executable is baked into the image.

Codex and Claude update independently. Updating one named volume does not replace or restart active sessions; the next
process start observes the current executable selected by that volume.

### 5. Host MCP Forwarding

Preflight forms one canonical union of loopback HTTP endpoints from trusted host configuration:

- Codex: the base `mcp_servers` table in the resolved Codex `config.toml`;
- Claude user scope: top-level `mcpServers` in the resolved global `.claude.json`;
- Claude local scope: `projects[<worktree>].mcpServers` in the same host file.

The union deduplicates identical host/port destinations, rejects listener collisions, participates in the creation
fingerprint, and uses the existing confined relay sidecar. Source-qualified names appear in the startup banner so the
operator can identify which product requested each endpoint.

Repository-controlled `.mcp.json`, plugin-provided configuration, and managed system configuration do not widen host
loopback access during launcher preflight. Public or LAN endpoints continue through normal container egress, and stdio
servers execute inside the container.

`--no-host-mcp` bypasses discovery for both products. A malformed trusted host configuration fails preflight rather
than silently dropping a configured loopback capability.

## Boundaries and Non-Goals

- The common container is Linux/Sysbox-only; this does not add a Docker Desktop session backend.
- The official installer and Anthropic release service are trusted update dependencies.
- Claude Code background sessions that outlive their foreground CLI are not a separate container-lifetime lease.
- The design does not coordinate file edits, allocate per-agent nested Docker namespaces, or merge conversation state.
- Automatic migration of existing ignored project config is deliberately excluded.

## Test Plan

- Command tests cover argv precedence, update dispatch, exit codes, and avoidance of Git/Sysbox on update.
- Resolver tests cover default and explicit Claude config paths, optional-state warnings, typed config, and mount modes.
- Host-MCP tests cover Claude user/local JSON, cross-product deduplication, and collision/failure behavior.
- Docker request tests prove both installation volumes are read-only, the default split paths remain native, and an
  explicit `CLAUDE_CONFIG_DIR` reaches every command.
- An ordinary-Docker disposable-volume probe proves the official installer creates the absolute executable.
- Real Sysbox smoke tests prove Claude state round-trip, volume execution, read-only installation, and reuse of one
  session container across Codex and Claude product commands.

## Where the Code Lives

- `cmd/claude-safe/`: public launch and update dispatch.
- `internal/cli/`, `internal/launchcli/`: shared parsing and product-specific resolved argv selection.
- `internal/launcher/`: Claude command, state mounts, update request, volume, fingerprint, and host-MCP preflight.
- `internal/launcher/hostmcp/`: Codex-plus-Claude trusted configuration discovery and canonical endpoint union.
- `container/claude-safe-update`, `container/Dockerfile`: official installer wrapper and session environment.
- `tests/smoke/`: real Docker/Sysbox integration boundary.
