// Package mcpchannel holds the wire contract of the host-MCP channel: the socket names inside one
// generation directory, and the one-byte control protocol spoken on control.sock.
//
// The launcher, the relay sidecar, and the session's `serve` are three processes implementing one
// private protocol across a bind mount. They agree here, in a package with no dependencies of its
// own, so the container image can build the sidecar without pulling in launcher-side code and the
// two ends cannot drift apart.
package mcpchannel

import (
	"fmt"
	"io"
	"net"
)

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

// Endpoint is one forwarded endpoint as carried in AGENTS_SAFE_HOST_MCP.
type Endpoint struct {
	// Listen are the concrete container addresses `serve` binds for this endpoint.
	Listen []string `json:"listen"`
	// Socket is the absolute path, inside the session container, of this endpoint's socket.
	Socket string `json:"socket"`
}

// Pipe moves bytes between the two hops of the channel until each side closes, propagating a
// half-close rather than tearing the peer down, so a request/response exchange completes in both
// directions.
//
// Both forwarders are byte pipes. Neither parses HTTP, MCP frames, or TLS, which is what lets
// streamable HTTP, Server-Sent Events, and long-lived MCP sessions pass through unchanged and with
// no timeout of their own.
func Pipe(first, second net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(first, second)
		CloseWrite(first)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(second, first)
		CloseWrite(second)
		done <- struct{}{}
	}()
	<-done
	<-done
}

// CloseWrite half-closes a connection that supports it, so the peer sees EOF on its read side while
// this side can still receive.
func CloseWrite(connection net.Conn) {
	if half, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	}
}
