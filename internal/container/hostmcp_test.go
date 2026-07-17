package container

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

const testBound = 10 * time.Second

func discardLog() *log.Logger { return log.New(io.Discard, "", 0) }

// shortRoot keeps the channel's socket paths inside the 108-byte sockaddr_un limit, which a
// descriptive Go test name embedded by t.TempDir() is enough to breach on its own.
func shortRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "cs")
	if err != nil {
		t.Fatalf("temporary root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// fakeSidecar stands in for the relay: it serves control.sock and one endpoint socket, answering an
// endpoint connection with a canned reply so the forwarder's byte path is assertable.
type fakeSidecar struct {
	dir     string
	socket  string
	control net.Listener
	leases  chan net.Conn
}

func newFakeSidecar(t *testing.T) *fakeSidecar {
	t.Helper()
	dir := shortRoot(t)
	sidecar := &fakeSidecar{
		dir:    dir,
		socket: filepath.Join(dir, mcpchannel.SocketName(0)),
		leases: make(chan net.Conn, 4),
	}

	endpoint, err := net.Listen("unix", sidecar.socket)
	if err != nil {
		t.Fatalf("bind endpoint socket: %v", err)
	}
	t.Cleanup(func() { _ = endpoint.Close() })
	go func() {
		for {
			connection, err := endpoint.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				// Read to EOF, not to a newline, so a forwarder that dropped the half-close would hang
				// here instead of replying: that is what makes half-close propagation assertable.
				request, err := io.ReadAll(connection)
				if err != nil {
					return
				}
				_, _ = fmt.Fprintf(connection, "relayed:%s", strings.TrimSpace(string(request)))
			}()
		}
	}()

	control, err := net.Listen("unix", filepath.Join(dir, mcpchannel.ControlSocketName))
	if err != nil {
		t.Fatalf("bind control socket: %v", err)
	}
	sidecar.control = control
	t.Cleanup(func() { _ = control.Close() })
	go func() {
		for {
			connection, err := control.Accept()
			if err != nil {
				return
			}
			role := make([]byte, 1)
			if _, err := io.ReadFull(connection, role); err != nil {
				_ = connection.Close()
				continue
			}
			if role[0] == mcpchannel.RoleLease {
				sidecar.leases <- connection
				continue
			}
			_ = connection.Close()
		}
	}()
	return sidecar
}

func (sidecar *fakeSidecar) endpoint(listen ...string) mcpchannel.Endpoint {
	return mcpchannel.Endpoint{Listen: listen, Socket: sidecar.socket}
}

// freePort reserves a loopback port and releases it, so a test can bind it deliberately.
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	return port
}

func TestHostMCPForwarderCopiesBytesToItsChannel(t *testing.T) {
	sidecar := newFakeSidecar(t)
	port := freePort(t)
	address := net.JoinHostPort("127.0.0.1", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel, err := startHostMCP(ctx, ctx, []mcpchannel.Endpoint{sidecar.endpoint(address)}, discardLog())
	if err != nil {
		t.Fatalf("startHostMCP() error = %v", err)
	}
	defer channel.close()

	// Codex would dial exactly the address its URL names.
	connection, err := net.DialTimeout("tcp", address, testBound)
	if err != nil {
		t.Fatalf("the container listener must accept: %v", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(testBound))
	if _, err := fmt.Fprintf(connection, "request\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	mcpchannel.CloseWrite(connection)
	answer, err := io.ReadAll(connection)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if strings.TrimSpace(string(answer)) != "relayed:request" {
		t.Fatalf("response = %q, want the sidecar's reply", answer)
	}
}

// serve opens exactly one lease and holds it for the session's life.
func TestHostMCPOpensExactlyOneLease(t *testing.T) {
	sidecar := newFakeSidecar(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel, err := startHostMCP(ctx, ctx,
		[]mcpchannel.Endpoint{sidecar.endpoint(net.JoinHostPort("127.0.0.1", freePort(t)))}, discardLog())
	if err != nil {
		t.Fatalf("startHostMCP() error = %v", err)
	}
	defer channel.close()

	select {
	case lease := <-sidecar.leases:
		if lease == nil {
			t.Fatal("the lease must be a live connection")
		}
	case <-time.After(testBound):
		t.Fatal("serve must open the lease during bootstrap")
	}
	select {
	case <-sidecar.leases:
		t.Fatal("serve must open exactly one lease")
	case <-time.After(200 * time.Millisecond):
	}
}

// An explicitly configured address is all-or-nothing: the user named it.
func TestHostMCPFailsWhenAnExplicitListenerCannotBind(t *testing.T) {
	sidecar := newFakeSidecar(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer taken.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = startHostMCP(ctx, ctx, []mcpchannel.Endpoint{sidecar.endpoint(taken.Addr().String())}, discardLog())
	if err == nil {
		t.Fatal("a listener that cannot bind must fail serve")
	}
	if !strings.Contains(err.Error(), "bind host MCP listener") {
		t.Fatalf("the diagnostic must name the bind failure, got %v", err)
	}
}

// The localhost legs are derived rather than requested, so one unavailable family is survivable.
//
// 240.0.0.1 is unassignable and its bind fails with "cannot assign requested address", the same
// class of error addressFamilyUnavailable matches for a missing IPv6 stack, so it stands in for a
// loopback leg whose family the container lacks. The skip must be logged, and the available leg must
// still bind and serve.
func TestHostMCPLocalhostSkipsAnUnavailableLegAndLogsIt(t *testing.T) {
	sidecar := newFakeSidecar(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logs strings.Builder
	logger := log.New(&logs, "", 0)
	endpoint := mcpchannel.Endpoint{
		Listen: []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("240.0.0.1", port)},
		Socket: sidecar.socket,
	}
	channel, err := startHostMCP(ctx, ctx, []mcpchannel.Endpoint{endpoint}, logger)
	if err != nil {
		t.Fatalf("a derived leg that cannot bind must be skipped, not fail serve: %v", err)
	}
	defer channel.close()
	if len(channel.listeners) != 1 {
		t.Fatalf("listeners = %d, want only the available leg", len(channel.listeners))
	}
	if !strings.Contains(logs.String(), "skipping") || !strings.Contains(logs.String(), "240.0.0.1") {
		t.Fatalf("the skipped leg must be logged, got %q", logs.String())
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), testBound)
	if err != nil {
		t.Fatalf("the surviving leg must serve: %v", err)
	}
	_ = connection.Close()
}

// A localhost endpoint with both loopback families available binds both and forwards on each, which
// is the ordinary case the skip above is the exception to.
func TestHostMCPLocalhostBindsBothLoopbackLegs(t *testing.T) {
	if !ipv6LoopbackAvailable(t) {
		t.Skip("this host has no usable [::1], so the dual-bind case cannot be exercised")
	}
	sidecar := newFakeSidecar(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := mcpchannel.Endpoint{
		Listen: []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)},
		Socket: sidecar.socket,
	}
	channel, err := startHostMCP(ctx, ctx, []mcpchannel.Endpoint{endpoint}, discardLog())
	if err != nil {
		t.Fatalf("both loopback legs must bind: %v", err)
	}
	defer channel.close()
	if len(channel.listeners) != 2 {
		t.Fatalf("listeners = %d, want both loopback legs", len(channel.listeners))
	}
	// Both concrete addresses must reach the one socket for this endpoint.
	for _, address := range []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)} {
		connection, err := net.DialTimeout("tcp", address, testBound)
		if err != nil {
			t.Fatalf("leg %s must serve: %v", address, err)
		}
		_ = connection.Close()
	}
}

// An explicit literal binds only itself, never a second family.
func TestHostMCPLiteralBindsOnlyItself(t *testing.T) {
	sidecar := newFakeSidecar(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := mcpchannel.Endpoint{Listen: []string{net.JoinHostPort("127.0.0.1", port)}, Socket: sidecar.socket}
	channel, err := startHostMCP(ctx, ctx, []mcpchannel.Endpoint{endpoint}, discardLog())
	if err != nil {
		t.Fatalf("a literal endpoint must bind: %v", err)
	}
	defer channel.close()
	if len(channel.listeners) != 1 {
		t.Fatalf("a literal endpoint binds exactly one listener, got %d", len(channel.listeners))
	}
}

func ipv6LoopbackAvailable(t *testing.T) bool {
	t.Helper()
	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// If no leg binds at all there is nothing to forward, and that is a launch failure.
func TestHostMCPFailsWhenNoDerivedLegCanBind(t *testing.T) {
	sidecar := newFakeSidecar(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := mcpchannel.Endpoint{
		Listen: []string{net.JoinHostPort("240.0.0.1", port), net.JoinHostPort("240.0.0.2", port)},
		Socket: sidecar.socket,
	}
	if _, err := startHostMCP(ctx, ctx, []mcpchannel.Endpoint{endpoint}, discardLog()); err == nil {
		t.Fatal("an endpoint with no bindable leg must fail serve")
	}
}

// A sidecar that never binds must fail bootstrap with the lease's own diagnostic, not leave serve
// retrying forever for the launcher to blame on a readiness timeout.
func TestHostMCPInitialLeaseIsBoundedByTheBootstrapDeadline(t *testing.T) {
	dir := shortRoot(t)
	endpoint := mcpchannel.Endpoint{
		Listen: []string{net.JoinHostPort("127.0.0.1", freePort(t))},
		Socket: filepath.Join(dir, mcpchannel.SocketName(0)),
	}

	sessionContext, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	bootstrapContext, cancelBootstrap := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelBootstrap()

	started := time.Now()
	_, err := startHostMCP(sessionContext, bootstrapContext, []mcpchannel.Endpoint{endpoint}, discardLog())
	if err == nil {
		t.Fatal("a lease that never opens must fail bootstrap")
	}
	if !strings.Contains(err.Error(), "open host MCP lease") {
		t.Fatalf("the diagnostic must name the lease, got %v", err)
	}
	// The bound is the deadline itself, not merely "eventually". An implementation that ignored
	// cancellation for several seconds must fail this, so allow only a small margin over 150ms.
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("the initial lease must return close to its 150ms deadline, waited %s", elapsed)
	}
}

// The sidecar can die while the session lives. serve must reopen the lease in the background so a
// replacement sidecar is leased without replacing the session container, and it must do so without
// racing the supervisor's own shutdown.
func TestHostMCPReopensTheLeaseAfterTheSidecarDies(t *testing.T) {
	sidecar := newFakeSidecar(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel, err := startHostMCP(ctx, ctx,
		[]mcpchannel.Endpoint{sidecar.endpoint(net.JoinHostPort("127.0.0.1", freePort(t)))}, discardLog())
	if err != nil {
		t.Fatalf("startHostMCP() error = %v", err)
	}

	first := <-sidecar.leases
	// The sidecar dies, taking the lease with it.
	_ = first.Close()

	select {
	case second := <-sidecar.leases:
		if second == nil {
			t.Fatal("the reopened lease must be a live connection")
		}
	case <-time.After(4 * LeaseRetryInterval):
		t.Fatal("serve must reopen the lease after the sidecar dies")
	}

	// Closing while the retry loop is live is the ordinary shutdown ordering.
	channel.close()
}

// Closing the channel must terminate the lease retry loop even while every reconnect is failing --
// which is exactly what happens once the sidecar has removed control.sock. Before the done signal,
// the inner loop consulted only the parent context and would retry every 2s forever.
func TestHostMCPCloseTerminatesLeaseRetry(t *testing.T) {
	dir := shortRoot(t)
	// No sidecar exists, so every attach the retry loop makes will fail.
	channel := &hostMCPChannel{log: discardLog(), done: make(chan struct{})}

	// Prime the loop with a lease that is already closed, so it drops straight into the failing
	// retry loop rather than blocking on a live read.
	local, remote := net.Pipe()
	_ = remote.Close()
	channel.lease = local

	returned := make(chan struct{})
	go func() {
		channel.holdLease(context.Background(), filepath.Join(dir, mcpchannel.SocketName(0)))
		close(returned)
	}()

	// Let it reach the failing retry loop, then close the channel.
	time.Sleep(50 * time.Millisecond)
	channel.close()

	select {
	case <-returned:
	case <-time.After(4 * LeaseRetryInterval):
		t.Fatal("close() must terminate the lease retry loop, not leave it spinning")
	}
}

// An empty set is the zero-cost path.
func TestHostMCPEmptySetOpensNoLeaseAndNoListener(t *testing.T) {
	channel, err := startHostMCP(context.Background(), context.Background(), nil, discardLog())
	if err != nil {
		t.Fatalf("an empty set must not fail: %v", err)
	}
	if channel != nil {
		t.Fatal("an empty set must create no channel")
	}
	channel.close()
}

func TestHostMCPEndpointsFromEnvironment(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		endpoints, err := hostMCPEndpointsFromEnvironment(func(string) (string, bool) { return "", false })
		if err != nil || endpoints != nil {
			t.Fatalf("an absent variable means no forwarding, got %#v / %v", endpoints, err)
		}
	})
	t.Run("decoded", func(t *testing.T) {
		raw := `[{"listen":["127.0.0.1:64342","[::1]:64342"],"socket":"/run/codex-safe-host-mcp/e0.sock"}]`
		endpoints, err := hostMCPEndpointsFromEnvironment(func(string) (string, bool) { return raw, true })
		if err != nil {
			t.Fatalf("a valid set must decode: %v", err)
		}
		if len(endpoints) != 1 || len(endpoints[0].Listen) != 2 ||
			endpoints[0].Socket != "/run/codex-safe-host-mcp/e0.sock" {
			t.Fatalf("decoded = %#v", endpoints)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		if _, err := hostMCPEndpointsFromEnvironment(func(string) (string, bool) { return "{", true }); err == nil {
			t.Fatal("malformed JSON must fail bootstrap")
		}
	})
	t.Run("relative socket", func(t *testing.T) {
		raw := `[{"listen":["127.0.0.1:1"],"socket":"e0.sock"}]`
		if _, err := hostMCPEndpointsFromEnvironment(func(string) (string, bool) { return raw, true }); err == nil {
			t.Fatal("a relative socket path must fail bootstrap")
		}
	})
}
