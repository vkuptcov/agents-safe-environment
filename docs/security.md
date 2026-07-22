# Security

The project reduces the blast radius of local agent execution; it is not a complete sandbox or production security
boundary. The durable runtime contract lives in [Architecture](../ARCHITECTURE.md) and the
[safe-environment design](design-docs/agents-safe.md).

## Credential Rules

- Never commit credentials, tokens, private keys, cookies, or populated environment files.
- Use variable names and placeholders in docs, tests, and fixtures.
- Do not broaden host-home access implicitly to make a tool work. Local `.agents-safe/config.toml` may add narrow,
  explicit read-write directory mounts under the reviewed project-environment contract; never configure the host
  home or filesystem root as a broad shortcut.
- Do not mount the host Docker socket into the outer container.
- Treat Git includes and credential helpers as separate host paths; mounting `.gitconfig` does not authorize them.
- Treat mounted Codex and Claude Code state as credentials exposed to every process in the shared project container.
  Product homes are separate persistence stores, not isolation boundaries between the two agents.

## Privilege Boundary

Passwordless sudo reaches root inside the ephemeral Sysbox container. It must not imply host-root access, privileged
Docker mode, or access to host daemon state. Changes to mounts, runtime flags, device access, capabilities, network,
or credential exposure require a design-doc update and a real-host verification plan.

Local configured mounts deliberately widen the host filesystem visible to project code. The config is Git-ignored but
is stored in the writable worktree, so code in an active session can change what a later cold session requests. Review
the file before launch and expose only directories whose contents the project may read, modify, or delete.
