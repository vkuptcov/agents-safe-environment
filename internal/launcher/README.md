# Host launcher

Owns managed-container identity, lifecycle, reuse, user mounts, and command retry policy. It consumes a validated
launch plan and delegates host Docker command encoding and execution to `dockercli`.

Child packages:

- `launchplan/` — builds the validated filesystem contract from Git-project discovery.
- `projectenv/` — discovers the fixed `.agents-safe/` build context and names its stable project image.
- `dockercli/` — performs typed operations through the host Docker CLI.

## Non-test files

- `codex.go` — constructs the absolute command for the volume-backed Codex installation.
- `codex_update.go` — runs the official installer in an isolated maintenance container with the shared volume.
- `claude.go` — constructs the absolute Claude Code command and permission-mode precedence.
- `claude_update.go` — runs Anthropic's native installer against the independent shared volume.
- `command_execution.go` — executes the session wrapper and classifies bounded lifecycle retries.
- `container_lifecycle.go` — owns deterministic reuse, create-race, wait, ownership, and preflight policy.
- `docker_requests.go` — maps prepared launcher state to typed Docker create and exec requests, including managed
  Go and uv cache routing plus both read-only product installation volumes.
- `host_environment.go` — validates host identity and discovers Git, Codex, and Claude host state.
- `identity.go` — derives deterministic project keys and container names.
- `launcher.go` — constructs the launcher, resolves host and user-mount state, and coordinates one launch.
- `project_environment.go` — discovers, builds, validates, and pins project-derived images.
- `project_config.go` — builds the typed project defaults, including product state mounts and arguments.
