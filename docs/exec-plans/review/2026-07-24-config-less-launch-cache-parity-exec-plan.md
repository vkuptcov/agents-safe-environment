# Config-less launch matches `agents-safe init` auto-discovery

Status: in review
Owner: Vladimir Kuptsov
Created: 2026-07-24

## Problem

A launch from a project with no `.agents-safe/config.toml` falls back to `DefaultProjectConfig`, which
carries the standard role mounts but no dependency caches. `agents-safe init`, by contrast, seeds the
default config with `--host-caches=auto` discovery (Go build, Go module, and uv caches). The two paths
therefore disagree: a linked worktree that never had `config.toml` generated (the file is git-ignored,
so `git worktree add` does not carry it) launches with every dependency cache labeled `absent`, even
though the same host caches exist and init would have mounted them.

Concretely: container `agents-safe-…` for worktree `legal-inception-link-checks-and-doc-chunks` mounted
no uv/Go caches because its `.agents-safe/` held only `Dockerfile` and `.gitignore`, and `Load` returned
the bare defaults.

## Goal

Make a config-less launch resolve the same dependency caches `agents-safe init` would auto-discover, so
the effective launch configuration is equivalent to a freshly `init`-generated one. Only the config-less
case changes; a present `config.toml` (including one that deliberately declares no caches) stays
authoritative.

## Implementation Decisions

- Resolve configuration along exactly two paths, not three. A present `config.toml` is read as-is and is
  authoritative — including when it deliberately declares no caches. When the file is absent, the launch
  generates the same default `agents-safe init` would have written and keeps it in memory. There is no
  intermediate "cache-less default, then conditionally seed" step: the generated default already carries
  its `dependency_caches`.
- Share one generator between init and launch. `launchcli.GenerateDefaultConfig` builds the full default
  (`DefaultProjectConfig` + `ResolveHostCaches`) and is called by both `agents-safe init` (which encodes
  it to `config.toml`) and the config-less launch path (which keeps it in memory), so the two cannot
  drift. Init passes the user's `--host-caches` selection; launch is fixed to `auto`, the init default.
- Inject the generator as a callback into `launcher.ResolveProjectConfig` rather than importing
  `launchcli` from `launcher`. `launchcli` already imports `launcher`; the callback keeps the dependency
  edge one-way while `launcher` owns the "config present → read, absent → generate" branch. A nil
  generator (used by unit tests) falls back to the plain cache-less defaults.
- Use `context.Background()` for the probes. Both cache probes (`go env`, `uv cache dir`) impose their
  own 5s timeout, and the init path's context is itself `context.Background()` from `main`, so parity is
  exact and a config-less launch cannot hang.
- Do not persist a `config.toml` on launch. Equivalence is of the effective (in-memory) configuration;
  writing files remains the job of `agents-safe init`.

## Phases

### Phase 1 — Config-file presence helper

Status: done
Done when: `projectenv` exposes a presence check that mirrors `Load`'s own file test, with no logic
duplicated between the two.

- Extract `locateConfigFile` from `Load` and reuse it in a new exported `ConfigFileExists`.

### Phase 2 — Generate config-less defaults from init's generator

Status: done
Done when: a launch with no `config.toml` mounts the same caches init would, and a launch with a present
`config.toml` is byte-for-byte unchanged.

- Add a `ConfiglessConfigGenerator` callback parameter to `launcher.ResolveProjectConfig`; when the config
  file is absent and the callback is non-nil, use its result as the configuration instead of `Load`.
- Extract `launchcli.GenerateDefaultConfig` (`DefaultProjectConfig` + `ResolveHostCaches`) and call it
  from both `agents-safe init` and the `launchcli.ResolveConfig` callback with the `auto` selection.

### Phase 3 — Tests and design-doc update

Status: done
Done when: unit coverage pins both the config-less (caches seeded) and present-config (unchanged) paths,
and the dependency-cache design doc states the config-less parity rule.

## Validation Gates

- `gofmt -l` over changed Go files reports nothing.
- `go test ./internal/launcher/... ./internal/launchcli/... ./internal/cli/...` passes.
- `make lint` passes.
- `make check-docs` passes.

## Risks and Constraints

- The creation fingerprint includes the dependency caches, so a config-less container created before this
  change (no caches) will fingerprint-mismatch and be recreated on next launch. That is the intended
  outcome — recreation is what attaches the caches — and it is fail-closed, not silent.
- Auto-discovery runs the bounded probes on every config-less launch. Both are ≤5s and only run when no
  config file exists; a project that has run `init` never hits this path.

## Out of Scope

- Persisting a generated `config.toml` during launch.
- Any `--host-caches` selection surface for launch; config-less launch is fixed to `auto`, matching the
  init default.
