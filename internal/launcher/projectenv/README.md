# Project environment definition

Discovers the project-owned `.agents-safe/` build boundary and names its stable local image. This package has no
Docker dependency: launcher policy selects when to build and validate the result.

## Non-test files

- `environment.go` — fixed-context discovery and stable per-project image naming.
