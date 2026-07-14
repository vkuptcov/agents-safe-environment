// Package session implements the container-local lifetime protocol used by
// codex-safe-session serve and codex-safe-session run.
package session

import "time"

const (
	// DefaultSocketPath is private to one outer codex-safe container. It is never
	// mounted from the host or exposed to nested containers by the launcher.
	DefaultSocketPath = "/run/codex-safe/session.sock"

	// ProtocolVersion is recorded in the outer container labels. The wire
	// protocol deliberately consists only of one acknowledgement byte.
	ProtocolVersion = "1"

	// Acknowledgement tells a wrapper that its connection has been counted and
	// that it may start its command.
	Acknowledgement byte = 1

	// DefaultIdleTimeout is used both at manager startup and after the final
	// registered command disconnects.
	DefaultIdleTimeout = 5 * time.Second

	// DefaultStartupTimeout bounds how long a wrapper waits for privileged
	// container bootstrap and the manager socket to become ready.
	DefaultStartupTimeout = 60 * time.Second
)
