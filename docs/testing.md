# Testing

Select the smallest suite that proves the changed boundary, then run the repository-wide gate before declaring a
code change complete when practical.

## Required Gates

- Go source or unit-test changes: run `go test` for the affected package and `go vet` when the change can affect
  static correctness, then run `make lint` before completion.
- Cross-package, launcher, container, or lifecycle changes: run `make test`.
- Go tooling or lint-configuration changes: run `make lint` and `make test`.
- Docker image changes: run `make docker-build` in addition to the relevant Go checks.
- Codex update-path changes: run the dispatcher/updater shell syntax checks and an ordinary-Docker disposable-volume
  install/read proof in addition to the image build.
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
- Treat `make lint` as the repository-wide Go static-analysis gate. It uses the pinned GolangCI-Lint version from
  the isolated `tools` module through the compiled project-local `bin/golangci-lint`; it does not require a global
  installation.
- Use `make lint-n-fix` only as an opt-in local rewrite step. Review all resulting changes, then rerun `make lint`
  and the applicable test gate; the fix target is not itself a completion gate.
- Treat Docker, Sysbox, registry, and network failures as environment failures when they occur before tested code
  executes; report them separately from code regressions.
- Do not claim a real-host security or mount property from unit tests alone.
