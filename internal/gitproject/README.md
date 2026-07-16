# Git project discovery

Resolves canonical paths for the selected Git worktree and validates the primary-checkout relationship required to
support linked worktrees safely.

## Non-test files

- `discovery.go` — queries Git for canonical worktree and shared-metadata paths.
