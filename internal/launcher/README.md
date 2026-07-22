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
- `command_execution.go` — executes the session wrapper and classifies bounded lifecycle retries.
- `container_lifecycle.go` — owns deterministic reuse, create-race, wait, ownership, and preflight policy.
- `docker_requests.go` — maps prepared launcher state to typed Docker create and exec requests, including managed
  Go and uv cache routing plus the read-only Codex installation volume.
- `host_environment.go` — validates host identity and launcher configuration and discovers host Git configuration.
- `identity.go` — derives deterministic project keys and container names.
- `launcher.go` — constructs the launcher, resolves host and user-mount state, and coordinates one launch.
- `project_environment.go` — discovers, builds, validates, and pins project-derived images.
- `user_mounts.go` — resolves and validates Codex-home and personal-skills mount sources.
