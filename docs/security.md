# Security

The project reduces the blast radius of local agent execution; it is not a complete sandbox or production security
boundary. The durable runtime contract lives in [Architecture](../ARCHITECTURE.md) and the
[safe-environment design](design-docs/codex-safe.md).

## Credential Rules

- Never commit credentials, tokens, private keys, cookies, or populated environment files.
- Use variable names and placeholders in docs, tests, and fixtures.
- Do not broaden host-home access to make a tool work. Add a narrow, explicit, read-only mount only after design and
  security review.
- Do not mount the host Docker socket into the outer container.
- Treat Git includes and credential helpers as separate host paths; mounting `.gitconfig` does not authorize them.

## Privilege Boundary

Passwordless sudo reaches root inside the ephemeral Sysbox container. It must not imply host-root access, privileged
Docker mode, or access to host daemon state. Changes to mounts, runtime flags, device access, capabilities, network,
or credential exposure require a design-doc update and a real-host verification plan.

