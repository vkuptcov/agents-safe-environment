# Shared launcher CLI

Parses the common `codex-safe`, `claude-safe`, and `agents-safe` flags, discovers the selected Git project, builds its
launch plan, and delegates the command to the managed-container launcher. Each product binary supplies only its
command policy and user-facing configuration.

## Non-test files

- `cli.go` — shared flag parsing, dependency orchestration, diagnostics, and exit-code propagation.
