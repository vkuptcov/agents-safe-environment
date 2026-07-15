# Sysbox Smoke Test — Go Decomposition Review

Date: 2026-07-15

Scope:

- Reviewed artifact: the Go port of `tests/smoke/sysbox-linked-worktree.sh` introduced across commits
  `642a557` → `52ca335` on branch `session-monitor`.
- Files:
  - `tests/smoke/sysbox_linked_worktree_test.go` — scenario + assertions (153 lines)
  - `tests/smoke/sysbox_fixture_test.go` — fixture composition, probe starters, inline scripts (145 lines)
  - `tests/smoke/smoke_setup_test.go` — layout, host identity, artifacts, launcher process (168 lines)
  - `tests/smoke/docker_harness_test.go` — Moby client wrapper (136 lines)
  - `tests/smoke/test_helpers_test.go` — assertion/exec helpers (90 lines)
  - `tests/smoke/testdata/{environment,worktree,nested-docker}-probe.sh` — embedded probe scripts
- Baseline compared against: `tests/smoke/sysbox-linked-worktree.sh` (619 lines).
- Reviewer: Claude Opus 4.8.

## Verdict

**The decomposition is sound and a clear improvement in readability over the monolithic script.** The
`key=value` report + Go-side assertion pattern replaces the fragile `grep --line-regexp` matching with typed
checks and good failure messages; splitting the launcher, Docker, and layout concerns into their own harness
types is the right shape; and coverage is largely preserved (see deltas below).

The main cost of the port is *surface area*: it is now ~810 lines across 9 files versus ~620 across 2. Most of
that is justified, but there are several places where the abstraction earns nothing. The proposals below remove
duplicated assertions, a pass-through indirection layer, and hand-rolled polling loops **without reducing
coverage**. None are blocking; they are cleanups.

There is also one non-cosmetic robustness regression worth fixing (R1).

---

## Simplification proposals

Ranked by payoff. S1–S3 are the ones I would actually apply; S4–S7 are optional polish.

### S1 — Delete the `smokeFixture` pass-through layer (embed `*dockerHarness`)

**Where:** `sysbox_fixture_test.go:48-129`.

Seven methods on `smokeFixture` do nothing but forward to `fixture.docker.*`:

```go
func (fixture *smokeFixture) cleanup()                 { fixture.docker.close() }
func (fixture *smokeFixture) startHostSentinel()       { fixture.docker.startSentinel() }
func (fixture *smokeFixture) inspectOuter() ...         { return fixture.docker.inspectOuter() }
func (fixture *smokeFixture) managedContainers() ...    { return fixture.docker.managedContainers() }
func (fixture *smokeFixture) containersNamed(...) ...   { return fixture.docker.containersNamed(name) }
func (fixture *smokeFixture) waitForOuter()            { fixture.docker.waitForOuter() }
func (fixture *smokeFixture) waitForOuterRemoval()     { fixture.docker.waitForOuterRemoval() }
```

They add a layer of indirection with no added behavior.

**Change:** make `*dockerHarness` an embedded (anonymous) field of `smokeFixture` and delete all seven
wrappers. Method promotion keeps every existing call site (`fixture.inspectOuter()`,
`fixture.managedContainers()`, …) compiling unchanged — **zero call-site churn**.

```go
type smokeFixture struct {
	t        *testing.T
	project  projectLayout
	host     hostIdentity
	files    smokeArtifacts
	launcher *launcherHarness
	*dockerHarness            // was: docker *dockerHarness
}
```

Constructor: `dockerHarness: newDockerHarness(t, project)`; cleanup: `t.Cleanup(fixture.close)`.

Field access inside the assert methods also simplifies via promotion:
`fixture.docker.daemonID` → `fixture.daemonID`, `fixture.docker.names.sentinel` → `fixture.names.sentinel`,
`fixture.docker.client` → `fixture.client`, etc. (`sysbox_linked_worktree_test.go:88,126,134-135`).

**Subtlety (benign):** `smokeFixture` and `dockerHarness` both have `t` and `project` fields. On an embedded
field the *shallow* field wins, so `fixture.t` and `fixture.project` still resolve to `smokeFixture`'s own
fields — no ambiguity, no compile error. Promoted methods keep using the harness's own `docker.t`/`docker.project`
via their receiver.

*Net: −7 methods (~30 lines), no behavior change.* Keep `launcher` as a named field — `fixture.launcher.start(...)`
reads better than a promoted `fixture.start(...)`.

### S2 — Replace the hand-rolled poll loops with `require.Eventually`

**Where:** `docker_harness_test.go:111-135` (`waitForOuter`, `waitForOuterRemoval`).

testify `v1.11.1` is already a dependency and ships `require.Eventually`. The two deadline loops (~25 lines) each
collapse to one call:

```go
func (docker *dockerHarness) waitForOuter() {
	docker.t.Helper()
	require.Eventually(docker.t, func() bool {
		return len(docker.managedContainers()) == 1
	}, commandTimeout, 100*time.Millisecond,
		"timed out waiting for the deterministic outer container")
}

func (docker *dockerHarness) waitForOuterRemoval() {
	docker.t.Helper()
	require.Eventually(docker.t, func() bool {
		_, err := docker.client.ContainerInspect(docker.ctx, docker.names.outer)
		return client.IsErrNotFound(err)
	}, 20*time.Second, 250*time.Millisecond,
		"deterministic outer container was not removed after idle timeout")
}
```

Trade-off: the current `waitForOuterRemoval` calls `require.NoError` on any non-`NotFound` inspect error
mid-loop. Under `require.Eventually` a transient error just retries and a *persistent* one surfaces as a timeout
rather than the raw error. If you want to keep the precise message, use `require.EventuallyWithT` and assert
inside the tick. Either way this is a net simplification.

Leave `fixture.waitForFile` (`sysbox_fixture_test.go:91-109`) custom — it has a genuinely useful extra branch
(fast-fail with `process.diagnostics()` when the launcher dies before creating the file) that `Eventually`
can't express as cleanly.

### S3 — Make the embedded probes fact-collectors; assert once, in Go

**Where:** `testdata/environment-probe.sh`, `testdata/nested-docker-probe.sh`.

Today each probe asserts a fact with a `[[ … ]]` gate under `set -e` **and** writes the value to the report,
and then Go asserts the same value again. The expected values live in two places:

| Fact | Probe encodes | Go re-encodes |
|------|---------------|---------------|
| 256-color threshold | `(( colors >= 256 ))` | `require.GreaterOrEqual(colors, 256)` |
| sudoers mode | `[[ "$sudoers_mode" == 440 ]]` | `require.Equal("440", …)` |
| git marker equality | `[[ … == "$git_marker" ]]` | `require.Equal(host.gitMarker, …)` |
| default runtime | `[[ … == crun ]]` | `require.Equal("crun", …)` |
| nested/compose running | `[[ … == true ]]` | `require.Equal("true", …)` |

**Change:** in the probe, *collect and report* only; drop the value-comparison gates. Keep `set -Eeuo pipefail`
so that a failing *command* (e.g. `getent`, `stat` on a missing sudoers file, `sudo` when passwordless access is
broken) still aborts the probe and is caught by `waitForFile(ready)`. Assertions on *values* then live in exactly
one place — Go — where the failure messages are already better.

For example, the tail of `environment-probe.sh` shrinks from ~20 gated lines to the collection + report:

```sh
# collect (no [[ ]] gates)
passwd_home="$(getent passwd "$expected_user" | cut -d: -f6)"
sudo_uid="$(sudo --non-interactive id -u)"
sudoers_mode="$(stat -c '%a' /etc/sudoers.d/codex-safe-host)"
sudoers_writable=false; [[ -w /etc/sudoers.d/codex-safe-host ]] && sudoers_writable=true
git_writable=false; (printf '\n[test]\n' >>"$HOME/.gitconfig") 2>/dev/null && git_writable=true
colors="$(tput colors)"
# … then a single printf … > "$report"
```

Two caveats to call out rather than hide:

- The `tools=true/false` collapse (`environment-probe.sh`) reduces five tool checks to one opaque boolean, so a
  failure no longer tells you *which* of `less/make/rg/compose/make-completion` is missing. If you want the
  diagnostic back, report them as five keys (`tool_less=true`, …) and assert each in Go. This is the one place
  the port *lost* granularity versus the shell (which printed the missing command).
- Keep the probe's `set -e` — it is what turns a broken mount or missing binary into a visible failure once the
  redundant gates are gone.

### S4 — Fold three polling primitives into one (optional)

`fixture.waitForFile`, `docker.waitForOuter`, and `docker.waitForOuterRemoval` are three variations on
"poll a condition until a deadline." After S2 only `waitForFile` remains hand-rolled, so this mostly disappears
on its own. If you keep any custom loop, a single `pollUntil(deadline, interval, func() bool) bool` helper in
`test_helpers_test.go` removes the last copy of the ticker/timer boilerplate.

### S5 — Name the remaining magic timeouts (optional)

Timeouts are half-named: `smokeTimeout`/`commandTimeout` are consts (`sysbox_fixture_test.go:17-18`) but
`20*time.Second` (idle removal), `15*time.Second` (cleanup), `250*time.Millisecond`, and `100*time.Millisecond`
are inline literals in `docker_harness_test.go`. Promote the two second-scale ones to named consts
(`idleRemovalTimeout`, `cleanupTimeout`) so the timing budget reads in one place.

### S6 — Reduce the positional Go↔shell coupling for the nested-docker probe (optional)

`startNestedDockerProbe` (`sysbox_fixture_test.go:70-77`) passes **11 positional arguments** that the script
unpacks as `$1 … ${11}`. This is exactly the brittleness that made the original monolith hard to edit — a single
reordering silently misbinds every downstream argument with no error. The probe already receives some inputs as
named env vars (`CODEX_SAFE_HOST_UID/GID`). Passing the rest the same way (`REPORT=…`, `NESTED_NAME=…`, injected
via `process.Env`) makes the contract self-describing and reorder-proof. Worth it for the 11-arg probe; the
2-arg `reuseScript`/`waitScript` are fine positional.

### S7 — Minor consistency notes (optional)

- `reuseScript` and `waitScript` are inline Go constants while the three larger probes are `//go:embed`
  (`sysbox_fixture_test.go:131-145`). The "inline the tiny ones, embed the big ones" split is reasonable — no
  change needed, just noting it is a deliberate boundary, not an oversight.
- `environment-probe.sh` recomputes `$(whoami)`, `$(id -gn)`, `$HOME` in the final `printf` after already
  binding some of them to variables. Compute each once for symmetry.

---

## Robustness / correctness notes

### R1 — Container-name suffix is not unique across concurrent runs (regression vs shell)

**Where:** `docker_harness_test.go:44,52-57`.

```go
suffix := filepath.Base(project.root)      // project.root == t.TempDir()
…
sentinel: "codex-safe-host-sentinel-" + suffix,
nested:   "codex-safe-nested-"        + suffix,
compose:  "codex-safe-compose-"       + suffix,
```

`t.TempDir()` returns `<random-parent>/001`, so `filepath.Base` is the sequence number **`001`**, not a unique
token (verified on Go 1.26: `ROOT=/tmp/TestX2752946448/001`, `BASE=001`). The *outer* container name is safe —
it comes from `launcher.ProjectContainerName(uid, worktree)`, hashed off the unique worktree path — but the
sentinel/nested/compose names collapse to `codex-safe-…-001` for every run. The shell used
`run_id="$(date +%s)-$$-${RANDOM}"` and was genuinely unique.

Impact: two concurrent invocations of this smoke test on the same host would collide on `startSentinel`
(`ContainerCreate` → "name already in use"). Low probability for a serialized smoke run, but it is a silent
regression in a test whose whole job is Docker isolation.

**Fix:** derive the suffix from the unique parent instead of the sequence:

```go
suffix := filepath.Base(filepath.Dir(project.root))   // e.g. TestSysboxLinkedWorktreeGo2752946448
```

or reuse the project key/hash already computed for the outer name.

### R2 — Two small coverage drops versus the shell (informational)

- **Host-side edit of the nested marker** (`sysbox-linked-worktree.sh:580-584`) — the shell appended `host edit`
  to the Sysbox-created marker and re-read it, proving the host can still write files the container produced.
  Not ported. `requireHostOwnership` covers *ownership* of those files but not host *writability*.
- **Post-run host git-config marker check** (`sysbox-linked-worktree.sh:572-575`) is folded into the
  in-container `git_writable=false` assertion. That covers "container can't write it"; it no longer independently
  confirms the host file was left byte-for-byte intact. Minor.

Neither is a simplification concern — flagging so the drop is a decision, not an accident.

---

## Suggested order of application

1. **R1** — one-line fix, real robustness win.
2. **S1** — biggest readability win, zero call-site churn.
3. **S2** — removes ~40 lines of loop boilerplate using a dependency already present.
4. **S3** — de-duplicates the probe/Go assertions; decide the `tools` granularity trade-off first.
5. S4–S7 — polish, apply as convenient.

All proposals are mechanical and independently verifiable with `make test-smoke-go`.

---

## Implementation follow-up

Status: implemented on 2026-07-15.

- **R1 resolved.** Host and nested container names now use `launcher.ProjectKey`, so concurrent smoke runs no
  longer share the `001` suffix from the first `t.TempDir` allocation.
- **S1 applied with named composition.** The pass-through methods were removed, but `docker *dockerHarness`
  remains a named field. Explicit `fixture.docker.*` calls preserve the ownership boundary and avoid promoted
  fields with overlapping `t` and `project` names.
- **S2 declined.** `testify` runs an `Eventually` condition in a separate goroutine, while the existing Docker
  helpers call `require` with the test's `*testing.T`. That combination can call `FailNow` outside the test
  goroutine. Keeping the two explicit loops also preserves immediate Docker API errors.
- **S3 applied.** The environment and nested-Docker probes now collect observed values; Go owns the value
  assertions and their failure messages. The Git report contains the value read from Git rather than the
  expected input, and each required tool has its own report key.
- **S4 declined.** The remaining wait loops observe different failure states, so a common polling abstraction
  would hide useful process and Docker diagnostics.
- **S5 applied.** Cleanup and idle-removal deadlines have semantic constant names.
- **S6 applied with in-container environment injection.** The nested probe receives named variables through an
  explicit `env KEY=value ... bash -c ...` command. Host `process.Env` was not used because the launcher does not
  forward arbitrary host variables through `docker exec`.
- **S7 applied where relevant.** The environment probe computes each reported value once.
- **R2 resolved.** The Go smoke test again proves that the host can append to a nested-container-created file and
  that the mounted host Git config remains byte-for-byte unchanged.
- **Dependency isolation added.** `tests/smoke` is a separate Go module, so the Moby client and its transitive
  dependencies no longer appear in the application module. `make test` explicitly tests and vets both modules;
  no `go.work` file changes the behavior of ordinary root-module commands.
- **Legacy Bash harness removed.** The Go scenario is now the single maintained real-host smoke test. Historical
  references earlier in this review describe the Bash baseline that existed when the decomposition was reviewed.

Validation:

- `GOCACHE=/tmp/codex-go-cache make test`
- `GOCACHE=/tmp/codex-go-cache make test-smoke-go` (`TestSysboxLinkedWorktreeGo` passed in 31.88 seconds)
