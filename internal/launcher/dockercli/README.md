# Docker CLI adapter

Encodes typed container operations as host `docker` commands and translates CLI output into transport results and
diagnostics. It does not own managed-container identity, reuse, mount-selection, or retry policy.

## Non-test files

- `client.go` — defines the adapter, replaceable process runner, and bounded command diagnostics.
- `build.go` — encodes project-image builds and mount-free compatibility probes.
- `create.go` — encodes and executes detached container creation.
- `exec.go` — encodes and executes an interactive command in an existing container.
- `inspect.go` — inspects containers and verifies the required runtime and image.
- `request.go` — defines Docker-specific request and response types.
