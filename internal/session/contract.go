// Package session implements the container-local lifetime protocol used by
// codex-safe-session serve and codex-safe-session run.
package session

import "time"

const (
	// DefaultSocketPath is the in-container rendezvous path shared by the
	// `serve` entrypoint and every `run` wrapper. The entrypoint listens here
	// after bootstrap; wrappers connect here before starting their child command.
	// The launcher never mounts this path from the host, passes it as a host
	// argument, or exposes it to nested containers.
	DefaultSocketPath = "/run/codex-safe/session.sock"

	// ProtocolVersion is recorded in the managed container labels. The wire
	// protocol deliberately consists only of one acknowledgement byte.
	ProtocolVersion = "1"

	// Acknowledgement tells a wrapper that its connection has been counted and
	// that it may start its command.
	Acknowledgement byte = 1

	// StoppingMarkerName is created next to the socket after admission closes.
	// A wrapper uses it only to distinguish bootstrap from committed shutdown.
	StoppingMarkerName = "stopping"

	// DefaultIdleTimeout is used both at manager startup and after the final
	// registered command disconnects.
	DefaultIdleTimeout = 5 * time.Second

	// DefaultStartupTimeout bounds how long a wrapper waits for privileged
	// container bootstrap and the manager socket to become ready.
	DefaultStartupTimeout = 60 * time.Second
)
