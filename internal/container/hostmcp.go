package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

const (
	// hostMCPEnv carries the forwarded endpoint set, fixed at container creation. An absent or empty
	// value is the zero-cost path: no listener, no lease, and `serve` starts exactly as before.
	hostMCPEnv = "AGENTS_SAFE_HOST_MCP"

	// LeaseRetryInterval is how long `serve` waits between lease attempts. It must stay well below
	// the sidecar's initial-lease timeout, or a replacement sidecar gives up before the session's
	// next attempt and recovery livelocks. It is exported so the launcher can assert the design's
	// timeout inequality across both packages in one place.
	LeaseRetryInterval = 2 * time.Second

	// PreLeaseDeadline bounds account reconciliation, filesystem preparation, and the first lease
	// together.
	//
	// It is what makes the design's timeout ordering real rather than decorative. Nothing on this
	// path is otherwise bounded, so without it a slow useradd or visudo could outlast the sidecar's
	// initial-lease timeout, and no finite value on the sidecar side could prevent the session from
	// never being leased. It is the session's half of the cold-start bound; the launcher's
	// session-create timeout is the other half.
	PreLeaseDeadline = 20 * time.Second
)

// hostMCPEndpointsFromEnvironment decodes the forwarded endpoint set. A missing or empty variable
// means this session forwards nothing.
func hostMCPEndpointsFromEnvironment(lookup func(string) (string, bool)) ([]mcpchannel.Endpoint, error) {
	raw, found := lookup(hostMCPEnv)
	if !found || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var endpoints []mcpchannel.Endpoint
	if err := json.Unmarshal([]byte(raw), &endpoints); err != nil {
		return nil, fmt.Errorf("parse %s: %w", hostMCPEnv, err)
	}
	for _, endpoint := range endpoints {
		if len(endpoint.Listen) == 0 {
			return nil, fmt.Errorf("%s endpoint %q has no listen address", hostMCPEnv, endpoint.Socket)
		}
		if !filepath.IsAbs(endpoint.Socket) {
			return nil, fmt.Errorf("%s socket %q is not absolute", hostMCPEnv, endpoint.Socket)
		}
	}
	return endpoints, nil
}

// hostMCPChannel is the session side of the channel: one listener per concrete address, plus the
// lease that gives the sidecar its lifetime signal.
//
// The lease is replaced by the background retry loop and read by close, so it lives behind the
// mutex. Both run for the session's whole life, and the loop reconnecting while the supervisor shuts
// down is ordinary rather than exotic.
type hostMCPChannel struct {
	listeners []net.Listener
	log       *log.Logger

	// done is closed by close(). The retry loop selects on it during every wait and every failed
	// attach, so closing the channel terminates recovery even when reconnection keeps failing --
	// which it always does once the sidecar has removed control.sock.
	done chan struct{}

	mutex  sync.Mutex
	lease  net.Conn
	closed bool
}

// currentLease returns the lease the retry loop should watch, or nil once the channel is closed.
func (channel *hostMCPChannel) currentLease() net.Conn {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	return channel.lease
}

// adoptLease installs a reconnected lease. It reports false once the channel is closed, so a lease
// that arrives during shutdown is closed rather than leaked.
func (channel *hostMCPChannel) adoptLease(connection net.Conn) bool {
	channel.mutex.Lock()
	defer channel.mutex.Unlock()
	if channel.closed {
		_ = connection.Close()
		return false
	}
	channel.lease = connection
	return true
}

// startHostMCP binds every configured listener and opens the lease, in that order.
//
// It runs after account bootstrap and before dockerd, which keeps that daemon's own ready timeout
// out of the cold-start budget the sidecar's initial-lease timeout must exceed. Neither the lease
// nor the listeners need dockerd, so the placement costs nothing.
//
// The two contexts are deliberately different. sessionContext is the channel's own lifetime, which
// is the session's. bootstrapContext bounds only the first lease acquisition: without that split,
// a sidecar that never binds would leave serve retrying forever instead of failing bootstrap with
// its own diagnostic.
func startHostMCP(
	sessionContext context.Context,
	bootstrapContext context.Context,
	endpoints []mcpchannel.Endpoint,
	logger *log.Logger,
) (*hostMCPChannel, error) {
	if len(endpoints) == 0 {
		return nil, nil
	}
	channel := &hostMCPChannel{log: logger, done: make(chan struct{})}
	for _, endpoint := range endpoints {
		// The bind honours the bootstrap deadline; the accept loops it starts run for the session's
		// life under sessionContext.
		if err := channel.listen(bootstrapContext, sessionContext, endpoint); err != nil {
			channel.close()
			return nil, err
		}
	}
	// The lease is opened last, after the listeners prove they can serve. A session that promised
	// forwarding and cannot deliver it fails rather than starting Codex against endpoints that will
	// not answer.
	lease, err := dialLease(bootstrapContext, endpoints[0].Socket, logger)
	if err != nil {
		channel.close()
		return nil, err
	}
	channel.lease = lease
	go channel.holdLease(sessionContext, endpoints[0].Socket)
	return channel, nil
}

// listen binds one endpoint's concrete addresses and starts forwarding them to its socket.
//
// A configured literal is all-or-nothing, because the user named that address and a silent
// substitution would answer a question they did not ask. The two localhost legs are the exception:
// they are derived rather than requested, so at least one must bind and a leg whose address family
// the container lacks is logged and skipped. Failing there would let an unrelated host setting break
// every session for a user whose only mistake was writing the most natural form of the URL.
func (channel *hostMCPChannel) listen(
	bindContext context.Context,
	serveContext context.Context,
	endpoint mcpchannel.Endpoint,
) error {
	derived := len(endpoint.Listen) > 1
	bound := 0
	var config net.ListenConfig
	for _, address := range endpoint.Listen {
		// The bind observes the bootstrap deadline; a resolver or bind that would otherwise block
		// past it is interrupted rather than allowed to outlast the cold-start bound.
		listener, err := config.Listen(bindContext, "tcp", address)
		if err != nil {
			if derived && addressFamilyUnavailable(err) {
				channel.log.Printf("host MCP: skipping %s, its address family is unavailable here", address)
				continue
			}
			return fmt.Errorf("bind host MCP listener %s: %w", address, err)
		}
		channel.listeners = append(channel.listeners, listener)
		bound++
		go channel.forward(serveContext, listener, endpoint.Socket, address)
	}
	if bound == 0 {
		return fmt.Errorf("no host MCP listener could bind for %s", strings.Join(endpoint.Listen, ", "))
	}
	return nil
}

// addressFamilyUnavailable reports whether a bind failed because the container's network namespace
// has no such address family, rather than because the address is taken or forbidden.
func addressFamilyUnavailable(err error) bool {
	var systemError *net.OpError
	if !errors.As(err, &systemError) {
		return false
	}
	message := systemError.Err.Error()
	return strings.Contains(message, "address family not supported") ||
		strings.Contains(message, "cannot assign requested address")
}

// forward accepts on one container address and pipes each connection to this endpoint's socket.
//
// A dial failure affects only the connection that caused it, so a host MCP server that restarts is
// reachable again on the next connection without restarting the container. A closed listener ends
// the loop; a temporary accept error backs off and continues rather than silently disabling a
// listener that is still bound.
func (channel *hostMCPChannel) forward(ctx context.Context, listener net.Listener, socket, address string) {
	const (
		backoffStart = 5 * time.Millisecond
		backoffMax   = time.Second
	)
	backoff := backoffStart
	for {
		connection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if temporary, ok := err.(interface{ Temporary() bool }); ok && temporary.Temporary() {
				channel.log.Printf("host MCP: accept on %s failed temporarily, retrying in %s: %v", address, backoff, err)
				time.Sleep(backoff)
				if backoff *= 2; backoff > backoffMax {
					backoff = backoffMax
				}
				continue
			}
			channel.log.Printf("host MCP: accept on %s stopped: %v", address, err)
			return
		}
		backoff = backoffStart
		go func() {
			defer connection.Close()
			var dialer net.Dialer
			target, err := dialer.DialContext(ctx, "unix", socket)
			if err != nil {
				channel.log.Printf("host MCP: %s could not reach its channel: %v", address, err)
				return
			}
			defer target.Close()
			mcpchannel.Pipe(connection, target)
		}()
	}
}

// dialLease opens the single lease connection this session holds for its life.
//
// The initial acquisition is synchronous and bounded by the caller's deadline: only an established
// lease that is later lost enters the background retry loop. A sidecar that has not yet bound
// control.sock is tolerated by retrying inside the deadline; one that never binds fails bootstrap
// with that diagnostic rather than leaving the launcher to blame a readiness timeout.
func dialLease(ctx context.Context, endpointSocket string, logger *log.Logger) (net.Conn, error) {
	control := filepath.Join(filepath.Dir(endpointSocket), mcpchannel.ControlSocketName)
	var last error
	for {
		connection, err := attachLease(ctx, control)
		if err == nil {
			logger.Printf("host MCP: lease established")
			return connection, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("open host MCP lease on %s: %w (last attempt: %v)", control, ctx.Err(), last)
		case <-time.After(LeaseRetryInterval):
		}
	}
}

func attachLease(ctx context.Context, control string) (net.Conn, error) {
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", control)
	if err != nil {
		return nil, err
	}
	// The lease sends its role byte and no further data; its EOF is the sidecar's signal that this
	// session is gone.
	if _, err := connection.Write([]byte{mcpchannel.RoleLease}); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

// holdLease keeps the lease attached for the session's life, reopening it in the background when the
// sidecar dies. A later launcher recreates the sidecar under the same name, and this retry is what
// lets the replacement be leased without replacing the session container.
func (channel *hostMCPChannel) holdLease(ctx context.Context, endpointSocket string) {
	control := filepath.Join(filepath.Dir(endpointSocket), mcpchannel.ControlSocketName)
	for {
		lease := channel.currentLease()
		if lease == nil {
			return
		}
		_, _ = io.Copy(io.Discard, lease)
		_ = lease.Close()
		if ctx.Err() != nil {
			return
		}
		channel.log.Printf("host MCP: lease lost; retrying every %s", LeaseRetryInterval)
		for {
			// close() is checked on every wait, not only after a successful attach. Once the channel
			// is closing, the sidecar has removed control.sock, so every attach below would fail and
			// this loop would otherwise retry forever.
			select {
			case <-ctx.Done():
				return
			case <-channel.done:
				return
			case <-time.After(LeaseRetryInterval):
			}
			connection, err := attachLease(ctx, control)
			if err != nil {
				continue
			}
			if !channel.adoptLease(connection) {
				return
			}
			channel.log.Printf("host MCP: lease re-established")
			break
		}
	}
}

func (channel *hostMCPChannel) close() {
	if channel == nil {
		return
	}
	channel.mutex.Lock()
	alreadyClosed := channel.closed
	channel.closed = true
	lease := channel.lease
	channel.lease = nil
	channel.mutex.Unlock()
	if alreadyClosed {
		return
	}

	// Signal the retry loop before closing the lease, so a loop woken by the lease closing sees the
	// channel is done rather than trying to reconnect to a sidecar that is gone.
	close(channel.done)
	if lease != nil {
		_ = lease.Close()
	}
	for _, listener := range channel.listeners {
		_ = listener.Close()
	}
}
