# Design Docs Index

Use [README.md](README.md) for design-doc format and maintenance rules.

## Current Design Specs

- [Safe Environment for Running Codex Agents](codex-safe.md): host launcher, Codex CLI and state integration,
  Sysbox boundary, mounts, identity, nested Docker, and lifecycle contract.
- [Go Session Manager for Shared codex-safe Containers](go-session-manager.md): container-local registration,
  supervision, command execution, and idle shutdown.
- [Host MCP Access from codex-safe Containers](host-mcp-forwarding.md): discovery of loopback MCP servers, the
  Unix-socket channel and its two forwarding hops, the per-session host relay, and the widened network boundary.
- [Project-Specific Agent Environments](project-environments.md): project-owned derived images, automatic builds,
  BuildKit-owned caching, compatibility validation, and active-session reuse.
- [Project Launcher Configuration](project-launcher-configuration.md): typed project defaults, TOML serialization,
  CLI precedence, creation-time reuse fingerprinting, and local `.agents-safe/.gitignore` behavior.
- [Host-Backed Dependency Caches](host-backed-dependency-caches.md): explicit uv, Go, Maven, and Gradle cache
  directories, tool-specific sharing policies, and host-state trust boundaries.
