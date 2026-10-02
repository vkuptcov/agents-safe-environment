# Launch plan

Converts Git-project discovery into the validated worktree identity, working directory, ordered bind mounts, and
managed dependency-cache routing used by host container orchestration.

## Non-test files

- `plan.go` — defines, builds, normalizes, and validates the launch filesystem contract, including canonical Go and
  uv cache order, managed environment keys, and tmpfs target selection.
- `docker_storage.go` — resolves the nested-Docker storage selection: scope, volume name, and the
  identities each scope owns.
- `python_venv.go` — reserves root `.venv` for recognized Python projects and discovers existing marked environments.
