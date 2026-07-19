# Project Launcher Configuration

Status: Implemented

Scope:

- the generated `.agents-safe/config.toml` schema;
- project-config and CLI precedence for `codex-safe` and `agents-safe`;
- `.agents-safe/.gitignore` creation.

Image builds remain owned by [Project-Specific Agent Environments](project-environments.md). Mount topology, Codex
command policy, and container reuse remain owned by [Safe Environment](codex-safe.md).

## Purpose and Intent

### Proposal

`agents-safe init` writes one typed project config containing the effective launcher defaults. Later invocations read
that file, apply only explicitly supplied CLI overrides, validate the result, and launch with the resolved values.

This gives the user two things immediately:

- visibility: the project file shows the image, mount plan, host-MCP choice, and launcher-specific arguments;
- persistence: editing a value once replaces the need to pass the same flag on every invocation.

Parameters have two lifecycle classes:

| Class | Parameters | Running-container behavior |
| --- | --- | --- |
| Creation-time | image, mounts, host MCP | Must match; a mismatch fails without replacement. |
| Command-time | Codex and agent argv | Applied immediately through `docker exec`. |

`--project` is a bootstrap parameter: it selects the worktree and deterministic container identity before config
resolution begins.

### Default Values

| Area | Default | Meaning |
| --- | --- | --- |
| Bootstrap | `--project .` | Discover the Git worktree from the current directory. |
| Common | `image = "codex-safe-mvp:local"` | Base or direct session image. |
| Common | `no_host_mcp = false` | Forward eligible host MCP servers. |
| Common | resolved logical mount snapshot | Always describe the complete project/Git topology plus available optional host integrations. |
| Codex | `arguments = ['--sandbox', 'danger-full-access']` | Use the Sysbox container as the sandbox boundary. |
| Agents | no default argv | Require a command on every `agents-safe` invocation. |

### Resolution Order

```mermaid
flowchart TD
    Project["1. Resolve --project<br/>and discover the worktree"]
    Defaults["2. Build typed defaults<br/>for this project and host"]
    File{"3. config.toml exists?"}
    Overlay["Overlay TOML<br/>mounts replace the whole list"]
    Flags["4. Apply explicitly supplied<br/>launcher flags"]
    Validate["5. Validate and classify<br/>the resolved parameters"]
    Normalize["6. Normalize logical roles<br/>to physical mounts"]
    Running{"7. Container running?"}
    Compatible{"Creation-time<br/>parameters match?"}
    Reject["Fail closed<br/>finish the active session first"]
    Create["Create container with<br/>creation-time parameters"]
    Command["8. Apply command-time parameters<br/>through docker exec"]

    Project --> Defaults --> File
    File -->|yes| Overlay --> Flags
    File -->|no| Flags
    Flags --> Validate --> Normalize --> Running
    Running -->|no| Create --> Command
    Running -->|yes| Compatible
    Compatible -->|yes| Command
    Compatible -->|no| Reject
```

`--project` is the only bootstrap option: it must be resolved before the project config can be found. At the CLI
layer, an omitted flag changes nothing; only a flag explicitly present in argv overrides the file.

### Worked Example

For this repository, `agents-safe init` generates:

```toml
[common]
image = "codex-safe-mvp:local"
no_host_mcp = false

[[common.mounts]]
role = "host_git_config"
source = "/home/alex/.gitconfig"
target = "/home/alex/.gitconfig"
read_only = true
comment = "Optional: expose host Git identity and includes read-only."

[[common.mounts]]
role = "primary_checkout"
source = "/home/alex/sources/agents-safe-environment"
target = "/home/alex/sources/agents-safe-environment"
read_only = true
comment = "Required: expose the primary checkout for Git topology."

[[common.mounts]]
role = "common_git_dir"
source = "/home/alex/sources/agents-safe-environment/.git"
target = "/home/alex/sources/agents-safe-environment/.git"
read_only = false
comment = "Required: keep shared Git metadata writable."

[[common.mounts]]
role = "worktree"
source = "/home/alex/sources/agents-safe-environment-init-command-support"
target = "/home/alex/sources/agents-safe-environment-init-command-support"
read_only = false
comment = "Required: expose the selected worktree writable."

[[common.mounts]]
role = "codex_home"
source = "/home/alex/.codex"
target = "/home/alex/.codex"
read_only = false
comment = "Optional: persist host Codex state."

[[common.mounts]]
role = "personal_skills"
source = "/home/alex/.agents/skills"
target = "/home/alex/.agents/skills"
read_only = true
comment = "Optional: expose personal skills read-only."

[[common.mounts]]
role = "host_mcp_channel"
source = "runtime://host-mcp-channel"
target = "/run/codex-safe-host-mcp"
read_only = false
comment = "Optional: forward eligible host MCP endpoints."

[codex]
arguments = [
    '--sandbox', 'danger-full-access'
]

[agents]
```

### Success Criteria

- The first screen explains what is configured and the exact application order.
- The generated file always contains the three required project/Git roles; Docker receives their minimal normalized
  physical mount set.
- Project values persist until the user edits the file; explicit CLI values affect one invocation.
- Removing a required mount fails before Docker access; removing a degradable mount starts with an explicit warning.
- A running container is reused only when all creation-time parameters match.
- Invalid or stale paths fail before Docker creation.
- An explicit Codex sandbox choice is never overridden by configured defaults.

### Tradeoff

The file is an explicit host-specific snapshot. This makes the effective project settings inspectable and editable,
but a host-path change requires the user to update the file rather than silently accepting newly discovered state.

## Contract

### 1. Generated Files

`agents-safe init [--project PATH]` creates missing files without overwriting user content:

```text
<worktree>/.agents-safe/Dockerfile.sample
<worktree>/.agents-safe/config.toml
<worktree>/.agents-safe/.gitignore
```

`Dockerfile.sample` is an embedded text resource. `config.toml` is the TOML serialization of the resolved typed
defaults. Initialization reads project and host filesystem state but does not contact Docker or start a container.

`.agents-safe/.gitignore` contains:

```gitignore
*
!.gitignore
!Dockerfile
```

The worktree-root `.gitignore` is not modified. Only `.agents-safe/.gitignore` and an activated `Dockerfile` are
trackable by default.

### 2. Typed Schema

```go
type ProjectConfig struct {
	Common CommonConfig `toml:"common"`
	Codex  CodexConfig  `toml:"codex"`
	Agents AgentsConfig `toml:"agents"`
}

type CommonConfig struct {
	Image     string        `toml:"image"`
	NoHostMCP bool          `toml:"no_host_mcp"`
	Mounts    []MountConfig `toml:"mounts"`
}

type CodexConfig struct {
	Arguments []string `toml:"arguments"`
}

type AgentsConfig struct{}

type MountRole string

type MountConfig struct {
	Role     MountRole `toml:"role"`
	Source   string    `toml:"source"`
	Target   string    `toml:"target"`
	ReadOnly bool      `toml:"read_only"`
	Comment  string    `toml:"comment"`
}
```

`CommonConfig` applies to both launchers. `CodexConfig` contains default Codex argv. `AgentsConfig` is empty until
`agents-safe` has launcher-specific defaults. `MountRole` gives role constants and downstream launch-plan APIs one
shared domain type. `MountConfig.Comment` is serialized documentation and does not affect Docker arguments.

### 3. File Layering

The decoder starts from a complete default `ProjectConfig` and overlays the TOML file:

- omitted scalar or section: retain the typed default;
- present scalar: replace the typed default;
- omitted `common.mounts`: retain the resolved default mount snapshot;
- present `common.mounts`: replace the entire list; mounts are never merged by index, role, source, or target.

The config is authoritative when present. The launcher does not silently reinsert a deleted entry or replace a stale
path. It compares the resolved list with the host-derived default roles: a missing required role fails, while a
missing degradable role remains absent and produces the warning defined below. Present entries always undergo full
path and mode validation.

### 4. Parameter Classes and Active Containers

The creation-time fingerprint is the SHA-256 digest of one versioned canonical structure:

| Field | Canonical value |
| --- | --- |
| `schema_version` | Integer `1` for the initial schema; incremented whenever encoding or field meaning changes. |
| `image_reference` | Resolved requested image reference, before resolving or building an immutable image ID. |
| `image_override` | Whether an explicit `--image` bypasses the project Dockerfile, even when the reference is unchanged. |
| `mounts` | Ordered normalized physical filesystem binds, each containing canonical `source`, `target`, and `read_only`. |
| `no_host_mcp` | Resolved boolean after defaults, TOML, and explicit CLI overrides. |
| `host_mcp_endpoints` | Eligible endpoints as canonical `host:port` strings, sorted by host and then port. |

`mounts` uses the exact deterministic order passed to Docker after alias and nesting normalization. It excludes the
materialized `host_mcp_channel` bind because that bind has a random generation-directory source;
`host_mcp_endpoints` captures whether the channel is needed and what it forwards. Endpoint server names are excluded:
multiple names selecting the same canonical address do not change container capabilities. `no_host_mcp` remains a
separate field so disabled discovery and enabled discovery with no eligible endpoints have distinct fingerprints.

The canonical structure is encoded from ordered structs and slices, never maps or TOML bytes. The
`codex-safe.launch-config` container label stores the resulting 64-character lowercase hexadecimal SHA-256 digest.
The following values are deliberately excluded:

- logical mount roles, comments, redundant aliases, and original TOML ordering;
- configured and invocation command argv, including Codex sandbox arguments;
- the invocation working directory, which every `docker exec` supplies independently;
- built image IDs, Dockerfile contents, and mutable-tag resolution results;
- host-MCP server names, random channel-generation paths, and sidecar container/image identities;
- separate project-identity, host-UID, ownership, and manager-protocol fields, which are validated independently
  before the fingerprint; project paths still appear naturally in the normalized mount entries.

A running container is reusable only when its ownership, protocol, and creation-time fingerprint match the current
request.

On mismatch, the launcher fails with the running and requested fingerprints and asks the user to finish the active
session. It never silently uses stale creation-time settings, stops another command, or replaces the container. After
the active container exits and is removed, the next invocation creates one from the resolved config.

Command-time parameters are the configured and invocation argv for `codex-safe` or `agents-safe`. They are not part of
the creation-time fingerprint and are applied to every command through `docker exec`, including commands entering a
reused container.

Every future config field must declare one of these classes. A creation-time field must participate in the
fingerprint; a command-time field must not.

### 5. CLI and Command Precedence

After file layering, only explicitly supplied launcher flags override the config. In particular:

- an omitted `--no-host-mcp` preserves `common.no_host_mcp`;
- `--no-host-mcp` sets it to `true` for one invocation;
- `--no-host-mcp=false` sets it to `false` for one invocation;
- an omitted `--image` preserves `common.image` and does not set explicit-image intent;
- an explicit `--image` replaces the image and bypasses `.agents-safe/Dockerfile` for that invocation.

`codex.arguments` is default argv in native Codex form. Invocation arguments are combined using the
[Codex command policy](codex-safe.md#codex-executable-and-process): when invocation arguments explicitly select a
sandbox policy, the configured default sandbox pair is suppressed. Other arguments remain separate argv elements;
the launcher does not invoke a shell.

### 6. Mount Serialization

`common.mounts` serializes logical mount roles whose topology and required modes are owned by
[Safe Environment](codex-safe.md#3-mount-plan). It is not a one-to-one copy of Docker's physical bind mounts:
validation first checks the complete logical topology, then normalization removes exact aliases and redundant nested
mounts without weakening their requested access mode. `role` makes validation independent of list position and
explains why access exists. Supported roles are:

- `host_git_config`, `primary_checkout`, `common_git_dir`, `worktree`;
- `codex_home`, `personal_skills`, `host_mcp_channel`;
- `additional` for an explicit user-added mount.

Default roles have one fixed absence policy. `MountConfig.Comment` begins with `Required:` or `Optional:` so the
generated file exposes that policy, but editing the comment never changes it. `ro` and `rw` below are the required
logical access modes; normalization may satisfy several logical roles with one physical `rw` mount.

| Role | Default when | Mode | If absent | Motivation |
| --- | --- | --- | --- | --- |
| `worktree` | Always. | `rw` | Stop. | No project files or valid working directory. |
| `primary_checkout` | Always. | `rw` when it equals `worktree`; otherwise `ro` | Stop. | The complete checkout topology must remain explicit; a distinct primary tree is visible without allowing branch-file changes there. |
| `common_git_dir` | Always. | `rw` | Stop. | Git refs, indexes, locks, and worktree metadata must remain writable. |
| `host_git_config` | Host file exists. | `ro` | Warn and continue. | Host identity/includes/defaults disappear. |
| `codex_home` | Host directory exists. | `rw` | Warn and continue. | Codex works with ephemeral state but loses host state. |
| `personal_skills` | Skills directory exists. | `ro` | Warn and continue. | Core execution works without personal skills. |
| `host_mcp_channel` | Host MCP enabled. | `rw` | Warn and continue. | Project works without forwarded host services. |
| `additional` | Never generated. | configured `ro` or `rw` | Ignore. | Deletion explicitly removes user-requested access. |

The three project/Git roles are always generated and required. Their default paths and modes are:

| Checkout kind | `worktree` | `primary_checkout` | `common_git_dir` | Physical result |
| --- | --- | --- | --- | --- |
| Regular | checkout root, `rw` | same checkout root, `rw` | `<checkout>/.git`, `rw` | One `rw` mount for the checkout root. |
| Linked worktree | linked root, `rw` | primary root, `ro` | `<primary>/.git`, `rw` | Three mounts; the nested writable Git mount follows the read-only primary mount. |

For a regular checkout, the exact `worktree`/`primary_checkout` alias is deduplicated and the writable worktree mount
already exposes the nested writable `.git` directory, so no separate `common_git_dir` bind is needed. For a linked
worktree, none of the three roles is redundant. `docker inspect` therefore shows the normalized physical result, not
necessarily one entry per serialized logical role; every inspected bind must still be traceable to one or more
validated roles.

The launcher stops when any of the three required project/Git roles is missing because the authoritative file no
longer describes a complete topology that can be validated and normalized safely. It does not reconstruct a deleted
role from host discovery. It continues when the loss is limited to an optional host integration; the warning keeps
that degradation visible rather than silently changing behavior.

Warnings are deterministic one-line diagnostics on `stderr`, emitted after config validation and before any Docker
inspection, build, or create operation:

```text
<binary>: warning: mount role "host_git_config" is omitted; host Git identity and includes are unavailable
<binary>: warning: mount role "codex_home" is omitted; host Codex state is unavailable; using ephemeral state
<binary>: warning: mount role "personal_skills" is omitted; personal skills are unavailable
<binary>: warning: mount role "host_mcp_channel" is omitted; host MCP forwarding is disabled
```

Warnings use this role order and print at most once per invocation. If config removes a `codex_home` role present in
the current default snapshot, both binaries emit its warning because the container loses persistent Codex state. If
the host Codex home never existed and the default snapshot therefore omitted the role, only `codex-safe` emits the
Codex-specific warning; `agents-safe` starts without one. Other optional host state absent from the default snapshot
does not produce a deletion warning.

A present mount whose source is stale or invalid is not treated as an omission. It fails closed so a typo or host-path
change cannot silently broaden or redirect access. Optionality authorizes deletion of the entry, not invalid content.

An omitted degradable role is still a creation-time mount-plan change. A cold launch may start after warning, but a
running container created with that mount remains incompatible and is left untouched until its active session ends.

Filesystem sources and targets are canonical absolute paths. Required roles must match the discovered project and
host identity. In a regular checkout, `worktree` and `primary_checkout` must be the same writable path and
`common_git_dir` must be its writable `.git` directory. In a linked worktree, the primary checkout must be read-only
and its nested common Git directory writable. Other security-sensitive read-only modes cannot be weakened. Conflicting
duplicate targets, root sources, missing sources, invalid modes, and unsafe overlaps fail before Docker creation.

Normalization deduplicates exact source/target/mode aliases and removes a nested mount only when an already retained
parent exposes the same source subtree with the required mode. It never replaces `ro` with broader `rw` access. The
creation-time fingerprint is computed from this normalized physical mount list, not from redundant logical aliases.

The `host_mcp_channel` role is conditional. Its `runtime://host-mcp-channel` source is not treated as a filesystem
path. The launcher materializes it only when `common.no_host_mcp` is `false`, the role remains in the resolved list,
and eligible endpoints exist. Otherwise the session has no channel mount. Channel creation and lifetime remain owned by
[Host MCP Access](host-mcp-forwarding.md).

Mount changes apply only when a container is created. A different normalized physical mount plan causes a
creation-time fingerprint mismatch while the previous container is running.

### 7. Validation and Failure Behavior

The config must be a regular, non-symlink TOML file. Unknown keys, invalid types, an empty image, unsafe Codex argv,
missing required mount roles, and invalid present mounts fail before Docker launch. Missing degradable roles emit
warnings and remain absent. Both binaries validate the complete file, including the other launcher's section.

The file is loaded on every invocation. Creation-time changes require a matching container or a cold create;
command-time changes apply to the current command.

The file is local machine state and remains ignored. Project code can edit it through the writable worktree and
affect a later launch, so every writable source in the file must be treated as accessible to project code.

## Boundaries and Non-Goals

- Internal Docker runtime, timeout, naming, relay, and lifecycle constants are not user configuration.
- `--project`, positional agent commands, and one-invocation Codex arguments are not serialized.
- Config changes do not mutate a running container.
- The config is host-specific and must not contain credentials.

## Test Plan

- Initialization serializes the exact typed config and creates the exact local ignore file.
- The resolution-order tests distinguish omitted flags from explicit boolean values.
- Scalar overlay, omitted mounts, whole-list mount replacement, and validation of all three required project/Git
  roles are covered.
- Removing each required role fails before Docker access; removing each degradable role emits the exact warning and
  omits that mount from the effective plan.
- A regular checkout serializes all three required roles and normalizes them to one writable physical mount; a linked
  worktree normalizes them to the three expected physical mounts in safe parent-before-child order.
- An invalid or stale mount snapshot fails before Docker creation.
- Creation-time fingerprints are stable, exclude comments and command argv, and cover every immutable config field.
- A running-container fingerprint mismatch fails without reuse, stop, or replacement.
- Config image and explicit CLI-image intent retain distinct project-Dockerfile behavior.
- Explicit invocation sandbox arguments suppress the configured default sandbox pair.
- Host-MCP mount presence follows `no_host_mcp` and endpoint eligibility.
- Real Sysbox smoke compares the normalized physical plan with session-container `docker inspect` output and traces
  every physical bind back to its serialized logical role or roles.

Implementation requires focused Go tests, `make lint`, `make test`, `make check-docs`, and `make test-smoke-go` on a
host with Sysbox.

## Where the code lives

The authoritative ownership map is in [Architecture](../../ARCHITECTURE.md#core-modules). This design changes:

- `cmd/agents-safe/` and `cmd/codex-safe/`: launcher-specific typed defaults;
- `internal/cli/`: config and explicit-CLI precedence;
- `internal/launcher/projectenv/`: initialization, TOML loading, and project-config validation;
- `internal/launcher/launchplan/`: required mount-role validation;
- `internal/launcher/`: image, mount, host-MCP, and command application.
