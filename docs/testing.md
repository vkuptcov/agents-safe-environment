# Testing

Select the smallest suite that proves the changed boundary, then run the repository-wide gate before declaring a
code change complete when practical.

## Required Gates

- Go source or unit-test changes: run `go test` for the affected package and `go vet` when the change can affect
  static correctness.
- Cross-package, launcher, container, or lifecycle changes: run `make test`.
- Docker image changes: run `make docker-build` in addition to the relevant Go checks.
- Documentation-only changes: run `make check-docs`; Go tests are not required unless the task asks for them.
- Real Sysbox, mount, nested-Docker, or linked-worktree behavior: run `make test-smoke-go` on a compatible Linux host.

## Focused Commands

```bash
go test ./internal/launcher
go test ./internal/session
go test ./internal/container
go test ./cmd/codex-safe ./cmd/codex-safe-session
```

## Completion Rules

- Format changed Go files with `gofmt` before running tests.
- Treat Docker, Sysbox, registry, and network failures as environment failures when they occur before tested code
  executes; report them separately from code regressions.
- Do not claim a real-host security or mount property from unit tests alone.

