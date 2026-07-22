# Tech Debt Tracker

Track long-lived deferred work here. Only add items when a deferral is explicit in an execution plan or
implementation review.

Feature-specific findings should stay in `docs/reviews/feature-review/` until resolved, accepted, or promoted here.

| ID | Deferred On | Area | Debt | Impact | Suggested Fix | Source | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TD-1 | 2026-07-15 | Session lifecycle | `make test-smoke-go` intermittently fails a first cold-start exec with `137` (SIGKILL). | Flaky real-host gate; passes on retry. | Make startup admission robust to nested-dockerd readiness racing the idle timeout. | codex-launch exec plan | open |
| TD-2 | 2026-07-15 | Codex acceptance | Full credentialed Codex turn is unverified in CI. | Non-credentialed smoke cannot prove Codex uses mounted instructions/skills end to end. | Run `TestSysboxCodexCredentialedAcceptance` with a dedicated test account. | codex-launch exec plan | open |
| TD-3 | 2026-07-22 | macOS updater | `codex-safe` does not compile for Darwin because `internal/terminal` uses Linux-only `syscall.TCGETS`. | The Docker Desktop-compatible maintenance design cannot yet be invoked through `codex-safe update` on macOS. | Split terminal detection into Linux and Darwin implementations, then add a Darwin host-binary cross-build gate. | persistent Codex exec plan, Phase 6 | open |
| TD-4 | 2026-07-22 | Project images | A derived image can replace `PATH` and lose `/opt/agents-safe/codex/bin` while still passing compatibility validation. | `codex-safe` still uses its absolute path, but `agents-safe codex` and interactive shells may not resolve the volume-backed executable. | Make the volume-backed `PATH` entry part of derived-image compatibility validation and add a regression test. | persistent Codex exec plan, Phase 6 | open |
| TD-5 | 2026-07-22 | Sysbox tmpfs compatibility | Privileged bootstrap repeats Docker's `--tmpfs` mounts because Sysbox 0.7 can cover nested tmpfs targets with the later idmapped worktree bind. | The launcher must maintain a duplicate JSON environment contract and container-side mount path after the runtime no longer needs them. | After a supported Sysbox release passes the effective-filesystem and sentinel smoke assertions using Docker `--tmpfs` alone, remove `AGENTS_SAFE_TMPFS_MOUNTS` and `mountContainerTmpfs`; retain `--tmpfs` and the strengthened smoke proof. | project virtual-environment isolation exec plan, Phase 1 | open |
