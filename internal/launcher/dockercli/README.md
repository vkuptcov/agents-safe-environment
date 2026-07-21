# Docker CLI adapter

Encodes typed container operations as host `docker` commands and translates CLI output into transport results and
diagnostics. It does not own managed-container identity, reuse, mount-selection, or retry policy. It also does not
own Codex installation store policy (protocol, target selection, volume ownership, label validation): that lives in
`internal/codexinstall` and `internal/launcher`. This package only exposes the typed Docker primitives those callers
compose — daemon target inspection, volume inspect/create, and bind-versus-volume mount argv — without deciding when
to use them.

## Non-test files

- `client.go` — defines the adapter, replaceable process runner, and bounded command diagnostics.
- `build.go` — encodes project-image builds and routes their output to launcher diagnostics.
- `create.go` — encodes and executes detached container creation, including bind- and named-volume mount argv.
- `exec.go` — encodes and executes an interactive command in an existing container.
- `inspect.go` — inspects containers and verifies the required runtime and image.
- `daemon.go` — inspects the connected Docker daemon's operating system and architecture, independent of the host
  running the CLI.
- `volume.go` — inspects and creates named Docker volumes, distinguishing "not found" from a transport failure.
- `run_attached.go` — runs one container in the foreground with streamed output, for the trusted maintenance-container
  path; detects a deterministic-name conflict, preserves the child exit code, and stops its own container on
  cancellation.
- `request.go` — defines Docker-specific request and response types, including the bind/volume `Mount` kind.
