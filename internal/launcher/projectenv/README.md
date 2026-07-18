# Project environment definition

Discovers, validates, and hashes the project-owned `.agents-safe/` build context. This package has
no Docker dependency: launcher policy selects when to prompt, build, and validate a definition.

## Non-test files

- `environment.go` — context discovery, deterministic definition and cache-key hashing, and image naming.
