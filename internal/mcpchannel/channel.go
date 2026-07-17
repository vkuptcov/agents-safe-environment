// Package mcpchannel holds the wire contract of the host-MCP channel: the socket names inside one
// generation directory, and the one-byte control protocol spoken on control.sock.
//
// The launcher, the relay sidecar, and the session's `serve` are three processes implementing one
// private protocol across a bind mount. They agree here, in a package with no dependencies of its
// own, so the container image can build the sidecar without pulling in launcher-side code and the
// two ends cannot drift apart.
package mcpchannel

import "fmt"

// ControlSocketName is bound last and removed first. Its existence is a truthful signal that the
// whole endpoint set is bound, which is why nothing else may be named this.
const ControlSocketName = "control.sock"

// The control protocol is one byte in each direction. A connection announces its role; a probe
// additionally reads one readiness byte. The lease sends its role byte and no further data, and its
// EOF is the session's death certificate.
const (
	// RoleLease is sent by `serve` on the single connection it holds for the session's life.
	RoleLease byte = 'L'
	// RoleProbe is sent by a launcher asking whether the channel is ready.
	RoleProbe byte = 'P'
	// Ready answers a probe once every endpoint socket is bound and the lease exists.
	Ready byte = 'R'
	// NotReady answers a probe before the lease exists.
	NotReady byte = 'N'
	// Refused answers a second lease while one is already held.
	Refused byte = 'X'
)

// SocketName is the endpoint's socket file name, indexed over the launch's sorted endpoint set.
// Neither side derives a socket name from an address, so no escaping rule has to agree across the
// mount.
func SocketName(index int) string {
	return fmt.Sprintf("e%d.sock", index)
}

// Endpoint is one forwarded endpoint as carried in CODEX_SAFE_HOST_MCP.
type Endpoint struct {
	// Listen are the concrete container addresses `serve` binds for this endpoint.
	Listen []string `json:"listen"`
	// Socket is the absolute path, inside the session container, of this endpoint's socket.
	Socket string `json:"socket"`
}
