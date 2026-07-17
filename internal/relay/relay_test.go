package relay_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
	"github.com/vkuptcov/agents-safe-environment/internal/relay"
)

const testBound = 10 * time.Second

// sentinel is a stand-in host MCP server. It answers a request line with a response line, so the
// byte path is assertable in both directions and a half-close is required to complete it.
type sentinel struct {
	listener net.Listener
	dialed   atomic.Int64
	lastHost atomic.Value
}

func newSentinel(t *testing.T) *sentinel {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "the sentinel must bind loopback")
	server := &sentinel{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				line, err := bufio.NewReader(connection).ReadString('\n')
				if err != nil {
					return
				}
				_, _ = fmt.Fprintf(connection, "echo:%s", line)
			}()
		}
	}()
	return server
}

func (server *sentinel) address() string { return server.listener.Addr().String() }

// shortRoot returns a temporary directory with a deliberately short path.
//
// A Unix socket path cannot exceed 108 bytes, and t.TempDir() embeds the test's own name, so a
// descriptive test name is enough on its own to push control.sock past the limit and fail the bind
// with an opaque "invalid argument". The launcher validates this budget for real at preflight.
func shortRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "cs")
	require.NoError(t, err, "the short temporary root must be created")
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// generation creates an owned generation directory, as the launcher would.
func generation(t *testing.T) string {
	t.Helper()
	path := filepath.Join(shortRoot(t), "g-0123456789abcdef")
	require.NoError(t, os.Mkdir(path, 0o700), "the generation directory must be created 0700")
	return path
}

// start runs a relay and returns its generation plus the error it eventually exits with.
func start(t *testing.T, config relay.Config) (context.CancelFunc, <-chan error) {
	t.Helper()
	if config.InitialLeaseTimeout == 0 {
		config.InitialLeaseTimeout = testBound
	}
	config.Log = log.New(io.Discard, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx, config) }()
	t.Cleanup(cancel)
	return cancel, done
}

// awaitFile waits for the relay to publish a socket.
func awaitFile(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Lstat(path)
		return err == nil
	}, testBound, 5*time.Millisecond, "%s must appear", path)
}

func controlPath(generationDir string) string {
	return filepath.Join(generationDir, mcpchannel.ControlSocketName)
}

// lease opens the single lease connection `serve` holds for the session's life.
func lease(t *testing.T, generationDir string) net.Conn {
	t.Helper()
	connection, err := net.Dial("unix", controlPath(generationDir))
	require.NoError(t, err, "the lease must connect")
	_, err = connection.Write([]byte{mcpchannel.RoleLease})
	require.NoError(t, err, "the lease must send its role byte")
	return connection
}

// probe asks the channel whether it is ready, as a launcher does.
func probe(t *testing.T, generationDir string) byte {
	t.Helper()
	connection, err := net.Dial("unix", controlPath(generationDir))
	require.NoError(t, err, "the probe must connect")
	defer connection.Close()
	_, err = connection.Write([]byte{mcpchannel.RoleProbe})
	require.NoError(t, err, "the probe must send its role byte")
	answer := make([]byte, 1)
	_ = connection.SetReadDeadline(time.Now().Add(testBound))
	_, err = io.ReadFull(connection, answer)
	require.NoError(t, err, "the probe must read one readiness byte")
	return answer[0]
}

// nonce drives the byte path: through an endpoint socket, out to the sentinel, and back.
func nonce(t *testing.T, socket string, request string) string {
	t.Helper()
	connection, err := net.Dial("unix", socket)
	require.NoError(t, err, "the endpoint socket must accept a connection")
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(testBound))
	_, err = fmt.Fprintf(connection, "%s\n", request)
	require.NoError(t, err, "the request must be written")
	// The half-close is what lets the sentinel finish reading and answer.
	require.NoError(t, connection.(*net.UnixConn).CloseWrite(), "the write side must half-close")
	answer, err := io.ReadAll(connection)
	require.NoError(t, err, "the response must be readable")
	return strings.TrimSpace(string(answer))
}

func TestRelayCopiesBytesBothWaysWithHalfClose(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	_, done := start(t, relay.Config{Generation: generationDir, Endpoints: []string{server.address()}})
	awaitFile(t, controlPath(generationDir))

	socket := filepath.Join(generationDir, mcpchannel.SocketName(0))
	require.Equal(t, "echo:request-nonce", nonce(t, socket, "request-nonce"),
		"the relay must copy the request out and the response back")

	holder := lease(t, generationDir)
	require.NoError(t, holder.Close(), "the lease must close")
	require.NoError(t, <-done, "lease EOF ends the relay cleanly")
}

// control.sock cannot exist while the endpoint set is partial: that is what makes it a truthful
// readiness signal.
func TestRelayBindsControlSocketLastAndPartialFailureRemovesTheRest(t *testing.T) {
	generationDir := generation(t)

	// Occupy the second endpoint's path with a non-socket, so its bind fails.
	blocked := filepath.Join(generationDir, mcpchannel.SocketName(1))
	require.NoError(t, os.Mkdir(blocked, 0o700), "the blocking entry must be created")

	err := relay.Run(context.Background(), relay.Config{
		Generation:          generationDir,
		Endpoints:           []string{"127.0.0.1:1", "127.0.0.1:2"},
		InitialLeaseTimeout: testBound,
		Log:                 log.New(io.Discard, "", 0),
	})
	require.Error(t, err, "a partial bind must exit nonzero")

	require.NoFileExists(t, filepath.Join(generationDir, mcpchannel.SocketName(0)),
		"a partial bind removes the sockets it already bound")
	require.NoFileExists(t, controlPath(generationDir),
		"control.sock is never published when the endpoint set is partial")
	require.DirExists(t, generationDir, "a failed attempt preserves the generation directory")
	require.DirExists(t, blocked, "an unexpected non-socket entry is never removed")
}

func TestRelayProbeReportsNotReadyUntilTheLeaseExists(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	_, done := start(t, relay.Config{Generation: generationDir, Endpoints: []string{server.address()}})
	awaitFile(t, controlPath(generationDir))

	require.Equal(t, mcpchannel.NotReady, probe(t, generationDir), "a bound channel with no lease is not ready")
	holder := lease(t, generationDir)
	require.Eventually(t, func() bool { return probe(t, generationDir) == mcpchannel.Ready },
		testBound, 5*time.Millisecond, "the channel is ready once the lease exists")

	// Readiness never touches a host MCP server: probing a data socket would hand the real server a
	// connection that immediately closes.
	require.Zero(t, server.dialed.Load(), "no readiness check may dial a host MCP endpoint")

	require.NoError(t, holder.Close(), "the lease must close")
	require.NoError(t, <-done, "lease EOF ends the relay cleanly")
}

func TestRelayRefusesASecondLeaseWhileOneIsHeld(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	_, done := start(t, relay.Config{Generation: generationDir, Endpoints: []string{server.address()}})
	awaitFile(t, controlPath(generationDir))

	holder := lease(t, generationDir)
	require.Eventually(t, func() bool { return probe(t, generationDir) == mcpchannel.Ready },
		testBound, 5*time.Millisecond, "the first lease must establish")

	second := lease(t, generationDir)
	defer second.Close()
	answer := make([]byte, 1)
	_ = second.SetReadDeadline(time.Now().Add(testBound))
	_, err := io.ReadFull(second, answer)
	require.NoError(t, err, "a refused lease must be answered")
	require.Equal(t, mcpchannel.Refused, answer[0], "a second lease is refused while one is held")

	require.NoError(t, holder.Close(), "the lease must close")
	require.NoError(t, <-done, "lease EOF ends the relay cleanly")
}

// Only EOF from an established lease proves the session holding the bind mount is gone.
func TestRelayEstablishedLeaseEOFRemovesTheGenerationAndExits(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	_, done := start(t, relay.Config{Generation: generationDir, Endpoints: []string{server.address()}})
	awaitFile(t, controlPath(generationDir))

	holder := lease(t, generationDir)
	require.Eventually(t, func() bool { return probe(t, generationDir) == mcpchannel.Ready },
		testBound, 5*time.Millisecond, "the lease must establish")
	require.NoError(t, holder.Close(), "the session's lease closes when the container dies")

	require.NoError(t, <-done, "lease EOF ends the relay cleanly")
	require.NoDirExists(t, generationDir, "established-lease EOF removes the generation directory")
}

func TestRelayInitialLeaseTimeoutRemovesSocketsButKeepsTheDirectory(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	before, err := os.Stat(generationDir)
	require.NoError(t, err, "the generation must be inspectable")

	err = relay.Run(context.Background(), relay.Config{
		Generation:          generationDir,
		Endpoints:           []string{server.address()},
		InitialLeaseTimeout: 50 * time.Millisecond,
		Log:                 log.New(io.Discard, "", 0),
	})
	require.Error(t, err, "no lease within the bound must exit nonzero")
	require.Contains(t, err.Error(), "no session lease", "the diagnostic names the missing lease")

	require.NoFileExists(t, controlPath(generationDir), "the timeout removes control.sock")
	require.NoFileExists(t, filepath.Join(generationDir, mcpchannel.SocketName(0)),
		"the timeout removes the endpoint sockets")
	after, err := os.Stat(generationDir)
	require.NoError(t, err, "the generation directory is preserved for a live session's bind mount")
	require.Equal(t, inode(t, before), inode(t, after), "the preserved directory keeps its inode")
}

// A signal while leased is a sidecar-only failure: the session is still alive and still holds the
// bind mount, so the directory must survive for a replacement to rebind inside it.
func TestRelaySignalWhileLeasedPreservesTheGenerationInode(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	before, err := os.Stat(generationDir)
	require.NoError(t, err, "the generation must be inspectable")

	cancel, done := start(t, relay.Config{Generation: generationDir, Endpoints: []string{server.address()}})
	awaitFile(t, controlPath(generationDir))
	holder := lease(t, generationDir)
	defer holder.Close()
	require.Eventually(t, func() bool { return probe(t, generationDir) == mcpchannel.Ready },
		testBound, 5*time.Millisecond, "the lease must establish")

	cancel()
	require.NoError(t, <-done, "a signal stops the relay cleanly")

	require.NoFileExists(t, controlPath(generationDir), "a signal removes the socket entries")
	after, err := os.Stat(generationDir)
	require.NoError(t, err, "a signal preserves the generation directory")
	require.Equal(t, inode(t, before), inode(t, after), "the preserved directory keeps its inode")
}

// A replacement sidecar rebinds in a generation the previous one left sockets in.
func TestRelayRemovesStaleSocketsBeforeBinding(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)

	// A stale socket, as a SIGKILLed sidecar would leave behind.
	stale, err := net.Listen("unix", filepath.Join(generationDir, mcpchannel.SocketName(0)))
	require.NoError(t, err, "the stale socket must be created")
	staleListener := stale.(*net.UnixListener)
	staleListener.SetUnlinkOnClose(false)
	require.NoError(t, staleListener.Close(), "the stale socket outlives its listener")

	_, done := start(t, relay.Config{Generation: generationDir, Endpoints: []string{server.address()}})
	awaitFile(t, controlPath(generationDir))
	require.Equal(t, "echo:live", nonce(t, filepath.Join(generationDir, mcpchannel.SocketName(0)), "live"),
		"the replacement rebinds over the stale socket and serves")

	holder := lease(t, generationDir)
	require.NoError(t, holder.Close(), "the lease must close")
	require.NoError(t, <-done, "lease EOF ends the relay cleanly")
}

// The relay mounts the parent, so it could reach a sibling. It must not.
func TestRelayRemovesOnlyItsOwnGenerationNeverAParentOrSibling(t *testing.T) {
	server := newSentinel(t)
	parent := shortRoot(t)
	own := filepath.Join(parent, "g-own")
	sibling := filepath.Join(parent, "g-sibling")
	require.NoError(t, os.Mkdir(own, 0o700), "the owned generation must be created")
	require.NoError(t, os.Mkdir(sibling, 0o700), "the sibling generation must be created")

	_, done := start(t, relay.Config{Generation: own, Endpoints: []string{server.address()}})
	awaitFile(t, controlPath(own))
	holder := lease(t, own)
	require.Eventually(t, func() bool { return probe(t, own) == mcpchannel.Ready },
		testBound, 5*time.Millisecond, "the lease must establish")
	require.NoError(t, holder.Close(), "the lease must close")
	require.NoError(t, <-done, "lease EOF ends the relay cleanly")

	require.NoDirExists(t, own, "the relay removes its own generation")
	require.DirExists(t, sibling, "the relay never removes a sibling generation")
	require.DirExists(t, parent, "the relay never removes the parent it mounts")
}

// The configured host reaches the resolver exactly as written, so localhost resolves as it would for
// host Codex rather than being silently rewritten to 127.0.0.1.
func TestRelayPassesLocalhostToTheResolverAsWritten(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	_, port, err := net.SplitHostPort(server.address())
	require.NoError(t, err, "the sentinel address must split")
	configured := net.JoinHostPort("localhost", port)

	dialed := make(chan string, 1)
	_, done := start(t, relay.Config{
		Generation: generationDir,
		Endpoints:  []string{configured},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case dialed <- address:
			default:
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	})
	awaitFile(t, controlPath(generationDir))
	require.Equal(t, "echo:x", nonce(t, filepath.Join(generationDir, mcpchannel.SocketName(0)), "x"),
		"the localhost endpoint must reach the sentinel")
	require.Equal(t, configured, <-dialed, "the relay dials the configured host as written")

	holder := lease(t, generationDir)
	require.NoError(t, holder.Close(), "the lease must close")
	require.NoError(t, <-done, "lease EOF ends the relay cleanly")
}

// A host MCP server that is down affects only the connection that tried to reach it.
func TestRelaySurvivesADialFailureAndKeepsAccepting(t *testing.T) {
	server := newSentinel(t)
	generationDir := generation(t)
	var fail atomic.Bool
	fail.Store(true)
	_, done := start(t, relay.Config{
		Generation: generationDir,
		Endpoints:  []string{server.address()},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if fail.Load() {
				return nil, fmt.Errorf("host MCP server is down")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	})
	awaitFile(t, controlPath(generationDir))

	socket := filepath.Join(generationDir, mcpchannel.SocketName(0))
	first, err := net.Dial("unix", socket)
	require.NoError(t, err, "the socket accepts even while the host server is down")
	_, _ = first.Write([]byte("x\n"))
	_ = first.SetReadDeadline(time.Now().Add(testBound))
	// The relay closes this connection with the request still unread, so the peer may see a reset
	// rather than a clean EOF. Either way nothing is relayed back, which is what Codex would report
	// as an unavailable MCP server.
	answer, _ := io.ReadAll(first)
	require.Empty(t, answer, "a failed dial relays nothing back")
	_ = first.Close()

	fail.Store(false)
	require.Equal(t, "echo:back", nonce(t, socket, "back"), "a later connection succeeds once the server returns")

	holder := lease(t, generationDir)
	require.NoError(t, holder.Close(), "the lease must close")
	require.NoError(t, <-done, "lease EOF ends the relay cleanly")
}

func TestRelayRejectsInvalidConfiguration(t *testing.T) {
	for name, config := range map[string]relay.Config{
		"relative generation": {Generation: "relative", Endpoints: []string{"127.0.0.1:1"}, InitialLeaseTimeout: testBound},
		"no endpoints":        {Generation: "/tmp/g", InitialLeaseTimeout: testBound},
		"no timeout":          {Generation: "/tmp/g", Endpoints: []string{"127.0.0.1:1"}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, relay.Run(context.Background(), config), "invalid configuration must be rejected")
		})
	}
}

func inode(t *testing.T, info os.FileInfo) uint64 {
	t.Helper()
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok, "the directory identity must be readable")
	return stat.Ino
}
