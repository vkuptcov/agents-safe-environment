# Docker CLI adapter

Encodes typed container operations as host `docker` commands and translates CLI output into transport results and
diagnostics. It does not own managed-container identity, reuse, mount-selection, update, or retry policy. It exposes
only the Docker primitives the launcher composes, including bind/volume mount encoding and attached container runs.

## Non-test files

- `client.go` — defines the adapter, replaceable process runner, and bounded command diagnostics.
- `build.go` — encodes project-image builds and routes their output to launcher diagnostics.
- `create.go` — encodes detached and attached container runs, including bind/volume mounts and entrypoint override.
- `exec.go` — encodes and executes an interactive command in an existing container.
- `inspect.go` — inspects containers and verifies the required runtime and image.
- `run_attached.go` — runs one container in the foreground with streamed output and exit-code preservation.
- `request.go` — defines Docker-specific request and response types, including separate bind and volume mounts.
