# Tech Debt Tracker

Track long-lived deferred work here. Only add items when a deferral is explicit in an execution plan or
implementation review.

Feature-specific findings should stay in `docs/reviews/feature-review/` until resolved, accepted, or promoted here.

| ID | Deferred On | Area | Debt | Impact | Suggested Fix | Source | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TD-1 | 2026-07-15 | Session lifecycle | `make test-smoke-go` intermittently fails a first cold-start exec with `137` (SIGKILL). | Flaky real-host gate; passes on retry. | Make startup admission robust to nested-dockerd readiness racing the idle timeout. | codex-launch exec plan | open |
| TD-2 | 2026-07-15 | Codex acceptance | Full credentialed Codex turn is unverified in CI. | Non-credentialed smoke cannot prove Codex uses mounted instructions/skills end to end. | Run `TestSysboxCodexCredentialedAcceptance` with a dedicated test account. | codex-launch exec plan | open |

