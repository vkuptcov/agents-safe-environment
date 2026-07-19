# Tech Debt Tracker

Track long-lived deferred work here. Only add items when a deferral is explicit in an execution plan or
implementation review.

Feature-specific findings should stay in `docs/reviews/feature-review/` until resolved, accepted, or promoted here.

| ID | Deferred On | Area | Debt | Impact | Suggested Fix | Source | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TD-1 | 2026-07-15 | Session lifecycle | `make test-smoke-go` intermittently fails a first cold-start exec with `137` (SIGKILL). | Flaky real-host gate; passes on retry. | Make startup admission robust to nested-dockerd readiness racing the idle timeout. | codex-launch exec plan | open |
| TD-2 | 2026-07-15 | Codex acceptance | Full credentialed Codex turn is unverified in CI. | Non-credentialed smoke cannot prove Codex uses mounted instructions/skills end to end. | Run `TestSysboxCodexCredentialedAcceptance` with a dedicated test account. | codex-launch exec plan | open |
| TD-3 | 2026-07-19 | Host | Double resolution. | I/O and drift. | See below. | cleanup | open |
| TD-4 | 2026-07-19 | Plan | Duplicate roles. | Drift risk. | See below. | cleanup | open |

## TD-3: Reuse One Host Snapshot During Launch

Current state:

- `launchcli.ResolveConfig` calls `launcher.ResolveHostEnvironment` to build project defaults and the logical mount
  snapshot.
- After configuration succeeds, `cli.Run` lazily calls `launcher.NewDockerLauncher`.
- `NewDockerLauncher` calls `resolveHostIdentity` again. That repeats canonical home resolution and host Git-config
  discovery even though launcher construction only retains `HomeDir`.

Impact:

- A normal launch performs duplicate filesystem and account-adjacent discovery.
- The second read can observe a different symlink or Git-config state from the snapshot used to build mount targets.
- The comment that launcher construction resolves only the host home is inaccurate because `resolveHostIdentity`
  also calls `DiscoverGitConfig`.

Suggested implementation:

1. Include the canonical host home, or a narrowly typed immutable host snapshot, in the resolved CLI configuration.
2. Change the lazy launcher factory to accept that resolved value instead of rediscovering it.
3. Split canonical home resolution from optional Git/Codex/skills discovery so each caller requests only what it uses.
4. Keep UID, GID, account-name, terminal, stream, and Docker-runner initialization lazy after config resolution.

Acceptance criteria:

- One ordinary launch invokes canonical host-home discovery once.
- The home used by generated mount targets, container environment, and `docker exec` is identical by construction.
- Help, usage errors, and invalid project configuration still return before launcher construction.
- Focused tests prove that launcher construction does not read Git config a second time.

## TD-4: Remove the Duplicate General Role Set from Plan

Current state:

- `launchplan.Plan.Provenance` records the roles satisfied by each normalized filesystem mount.
- `launchplan.Plan.Roles` separately records every retained logical role, including the same filesystem roles.
- The only production `HasRole` call checks `host_mcp_channel`, the sole current non-filesystem role.

Impact:

- Filesystem roles have two representations that must remain synchronized during normalization and tests.
- `Resolve` builds and deduplicates a general role slice solely to answer one host-MCP capability question.
- Future role changes can accidentally update provenance without updating the separate role set, or vice versa.

Suggested implementation:

1. Replace `Plan.Roles` and generic `HasRole` with an explicit field such as `HostMCPChannel bool`.
2. Continue using `MountProvenance` for filesystem-role lookup through `MountForRole`.
3. Set the capability directly from the validated `host_mcp_channel` config role during `Resolve`.
4. Remove `containsRole` and the role-slice construction path when no other non-filesystem role requires it.

Acceptance criteria:

- Host-MCP planning preserves enabled, omitted, and `--no-host-mcp` behavior.
- Codex-home and personal-skills mount lookup still derives from normalized provenance.
- Creation fingerprints remain unchanged because logical roles are intentionally excluded.
- `Plan` has one representation for filesystem-role provenance and explicit state for non-filesystem capabilities.
