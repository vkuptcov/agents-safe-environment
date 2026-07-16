# Container bootstrap

Everything in this package runs inside or configures the Sysbox container. It does not configure the Docker host.
The package recreates the invoking host identity and home inside the container, starts the private Docker daemon,
and supervises the container-local session manager.

## Non-test files

- `account.go` — reconciles container-local user and group entries with the invoking host identity.
- `config.go` — reads and validates the launcher-provided environment contract.
- `dockerd.go` — starts, probes, and stops the private Docker daemon.
- `paths.go` — defines image-owned and writable runtime paths inside the container.
- `supervisor.go` — coordinates bootstrap, dockerd, and the session manager lifecycle.
- `user_filesystem.go` — prepares the container-local home, Bash configuration, and sudoers policy.
