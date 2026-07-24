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

- Gate the new behavior strictly on config-file absence. A present `config.toml` already owns the cache
  list, and TOML omission of `[[common.dependency_caches]]` must keep meaning "inherit the (cache-less)
  defaults", not "auto-discover". Seeding caches into the defaults unconditionally would let a
  present-but-omitted config silently inherit auto caches, so the seed only applies when no file exists.
- Discover through the existing init machinery (`launchcli.ResolveHostCaches` with the `auto` selection),
  not a second implementation, so launch and init cannot drift.
- Inject discovery as a callback into `launcher.ResolveProjectConfig` rather than importing `launchcli`
  from `launcher`. `launchcli` already imports `launcher`; the callback keeps the dependency edge
  one-way while letting `launcher` own the "only when absent" policy in its single defaults→Load→plan
  flow.
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

### Phase 2 — Seed config-less defaults with auto-discovered caches

Status: done
Done when: a launch with no `config.toml` mounts the same caches init would, and a launch with a present
`config.toml` is byte-for-byte unchanged.

- Add a `DefaultCacheDiscoverer` callback parameter to `launcher.ResolveProjectConfig`; when the config
  file is absent and the callback is non-nil, set the discovered caches on the defaults before `Load`.
- Wire the callback in `launchcli.ResolveConfig` to `ResolveHostCaches` with the `auto` selection.

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
