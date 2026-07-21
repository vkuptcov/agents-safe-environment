# Launch plan

Converts Git-project discovery into the validated worktree identity, working directory, ordered bind mounts, and
managed dependency-cache routing used by host container orchestration.

## Non-test files

- `plan.go` — defines, builds, normalizes, and validates the launch filesystem contract, including canonical Go and
  uv cache order and their managed environment keys.
