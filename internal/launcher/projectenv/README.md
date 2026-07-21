# Project environment definition

Discovers the project-owned `.agents-safe/` build boundary and names its stable local image. This package has no
Docker dependency: launcher policy selects when to build and validate the result.

It also owns the typed local configuration. `common.dependency_caches` accepts the fixed `go_build`, `go_modules`,
and `uv` kinds; source discovery belongs to `launchcli`, while this package validates and serializes the snapshot.

## Non-test files

- `environment.go` — fixed-context discovery and stable per-project image naming.
- `config.go` — typed TOML configuration, including supported dependency-cache kinds.
- `init.go` — lazy project-environment initialization that preserves an existing cache snapshot.
