# Session lifecycle

Implements the container-local protocol that registers managed commands, preserves their exit status and streams, and
lets the container stop after the final command becomes idle.

## Non-test files

- `command.go` — registers, runs, and signal-forwards one managed command.
- `contract.go` — defines the manager socket and shutdown-marker contract.
- `manager.go` — accepts wrapper registrations and controls idle shutdown.
