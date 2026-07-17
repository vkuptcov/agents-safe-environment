// Package relay implements the host-MCP relay sidecar: the one container per session that holds the
// host network namespace, serves the channel's Unix sockets, and dials the host loopback endpoints.
//
// It exists because the session container cannot open a host loopback socket by itself. It runs no
// agent code, holds no capability, has a read-only root filesystem, receives no Docker socket, and
// mounts one directory. Its lifetime follows a lease held by `serve`, not the launcher that created
// it, so a command that outlives its launcher keeps working.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

// socketMode is the channel's access control, together with the 0700 generation directory and the
// mount itself. The kernel enforces both; no token or handshake would add anything against a peer
// that can already read them.
const socketMode = 0o600

// dialTimeout bounds one dial of a host MCP server. A dial failure affects only the connection that
// caused it, so this never ends the sidecar.
const dialTimeout = 10 * time.Second

// Config is one relay sidecar's whole input.
type Config struct {
	// Generation is the absolute generation directory as seen inside this container. The sidecar
	// mounts the project runtime parent, so this is a subdirectory of its mount and removable.
	Generation string
	// Endpoints are the host destinations in the launch's sorted order, which fixes each endpoint's
	// socket index. Each is dialed exactly as configured.
	Endpoints []string
	// InitialLeaseTimeout bounds the wait for the session's first lease. Expiry removes the socket
	// entries but preserves the directory, because a live session may already have it bind-mounted.
	InitialLeaseTimeout time.Duration
	// Log receives endpoint, connection lifecycle, and errors. It never receives MCP payloads or
	// request URLs.
	Log *log.Logger
	// Dial is the host dialer, replaceable in tests. Production leaves it nil.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

func (config Config) validate() error {
	if !filepath.IsAbs(config.Generation) {
		return fmt.Errorf("generation directory %q is not absolute", config.Generation)
	}
	if len(config.Endpoints) == 0 {
		return errors.New("at least one endpoint is required")
	}
	if config.InitialLeaseTimeout <= 0 {
		return fmt.Errorf("initial lease timeout must be positive, got %s", config.InitialLeaseTimeout)
	}
	return nil
}

func (config Config) dial(ctx context.Context, address string) (net.Conn, error) {
	if config.Dial != nil {
		return config.Dial(ctx, "tcp", address)
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	return dialer.DialContext(ctx, "tcp", address)
}

// Run serves the channel until the session's lease closes, the initial-lease timeout expires, or the
// context is cancelled by a signal.
//
// Only EOF from an established lease permits removing the generation directory: it proves the
// session process holding the bind mount is gone. Every other exit preserves the directory inode, so
// a replacement sidecar can rebind inside the mount a live session still holds.
func Run(ctx context.Context, config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	logger := config.Log
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	paths := socketPaths(config)
	if err := removeStaleSockets(paths, logger); err != nil {
		return err
	}

	channel, err := bind(config, logger)
	if err != nil {
		return err
	}
	defer channel.close()

	established, closed := channel.serveControl()
	channel.serveEndpoints(ctx, config, logger)
	logger.Printf("relay bound %d endpoint socket(s) and %s", len(config.Endpoints), mcpchannel.ControlSocketName)

	select {
	case <-established:
		logger.Printf("relay lease established")
	case <-time.After(config.InitialLeaseTimeout):
		return fmt.Errorf("no session lease within %s", config.InitialLeaseTimeout)
	case <-ctx.Done():
		logger.Printf("relay stopping before a lease was established")
		return nil
	}

	select {
	case <-closed:
		logger.Printf("relay observed lease EOF; the session is gone")
		// Close the sockets before unlinking the directory that holds them.
		channel.close()
		if err := os.RemoveAll(config.Generation); err != nil {
			return fmt.Errorf("remove generation %q: %w", config.Generation, err)
		}
		logger.Printf("relay removed its generation directory")
		return nil
	case <-ctx.Done():
		logger.Printf("relay stopping while leased; preserving the generation directory")
		return nil
	}
}

// socketPaths returns every path this sidecar owns, endpoint sockets first and control.sock last.
func socketPaths(config Config) []string {
	paths := make([]string, 0, len(config.Endpoints)+1)
	for index := range config.Endpoints {
		paths = append(paths, filepath.Join(config.Generation, mcpchannel.SocketName(index)))
	}
	return append(paths, filepath.Join(config.Generation, mcpchannel.ControlSocketName))
}

// removeStaleSockets clears entries a previous sidecar left in this owned generation. An unexpected
// non-socket entry is an error and is never removed: this directory is bind-mounted into a live
// session, and deleting something the relay does not recognize is not its call.
func removeStaleSockets(paths []string, logger *log.Logger) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %q: %w", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("unexpected non-socket entry %q with mode %v", path, info.Mode())
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale socket %q: %w", path, err)
		}
		logger.Printf("relay removed stale socket %s", filepath.Base(path))
	}
	return nil
}

// channel is the bound socket set.
type channel struct {
	endpoints []net.Listener
	control   net.Listener
	paths     []string
	logger    *log.Logger
	closeOnce sync.Once
}

// bind binds every endpoint socket and then control.sock, in that order, and starts no accept loop.
// Startup is all-or-nothing: any failure removes the sockets already bound in this attempt, so no
// launcher can mistake a partial set for a ready channel, and control.sock is never published.
func bind(config Config, logger *log.Logger) (*channel, error) {
	bound := &channel{logger: logger}
	for index := range config.Endpoints {
		path := filepath.Join(config.Generation, mcpchannel.SocketName(index))
		listener, err := listenUnix(path)
		if err != nil {
			bound.close()
			return nil, fmt.Errorf("bind endpoint socket %q: %w", path, err)
		}
		bound.endpoints = append(bound.endpoints, listener)
		bound.paths = append(bound.paths, path)
	}
	controlPath := filepath.Join(config.Generation, mcpchannel.ControlSocketName)
	control, err := listenUnix(controlPath)
	if err != nil {
		bound.close()
		return nil, fmt.Errorf("bind control socket %q: %w", controlPath, err)
	}
	bound.control = control
	bound.paths = append(bound.paths, controlPath)
	return bound, nil
}

func listenUnix(path string) (net.Listener, error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Go creates the socket under the process umask, so the mode the channel relies on is applied
	// explicitly rather than assumed.
	if err := os.Chmod(path, socketMode); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

// close removes this sidecar's socket entries and preserves the generation directory.
func (bound *channel) close() {
	bound.closeOnce.Do(func() {
		for _, listener := range bound.endpoints {
			_ = listener.Close()
		}
		if bound.control != nil {
			_ = bound.control.Close()
		}
		// Closing a Go unix listener unlinks the path it created; make removal explicit so a failed
		// attempt cannot leave a partial endpoint set behind.
		for _, path := range bound.paths {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				bound.logger.Printf("relay could not remove %s: %v", filepath.Base(path), err)
			}
		}
	})
}

// serveControl answers probes and accepts exactly one lease. It reports when that lease is
// established and when it reaches EOF.
func (bound *channel) serveControl() (established <-chan struct{}, closed <-chan struct{}) {
	establishedSignal := make(chan struct{})
	closedSignal := make(chan struct{})
	go func() {
		var leaseHeld atomic.Bool
		var establishedOnce, closedOnce sync.Once
		for {
			connection, err := bound.control.Accept()
			if err != nil {
				return
			}
			go func() {
				role := make([]byte, 1)
				_ = connection.SetReadDeadline(time.Now().Add(30 * time.Second))
				if _, err := io.ReadFull(connection, role); err != nil {
					_ = connection.Close()
					return
				}
				_ = connection.SetReadDeadline(time.Time{})
				switch role[0] {
				case mcpchannel.RoleProbe:
					// Ready only once the lease exists, so a launcher never starts a command against
					// a channel whose container side has not attached yet.
					answer := mcpchannel.NotReady
					if leaseHeld.Load() {
						answer = mcpchannel.Ready
					}
					_, _ = connection.Write([]byte{answer})
					_ = connection.Close()
				case mcpchannel.RoleLease:
					if !leaseHeld.CompareAndSwap(false, true) {
						_, _ = connection.Write([]byte{mcpchannel.Refused})
						_ = connection.Close()
						return
					}
					establishedOnce.Do(func() { close(establishedSignal) })
					// The lease sends no further data; its EOF is what this sidecar waits for.
					_, _ = io.Copy(io.Discard, connection)
					_ = connection.Close()
					leaseHeld.Store(false)
					closedOnce.Do(func() { close(closedSignal) })
				default:
					_ = connection.Close()
				}
			}()
		}
	}()
	return establishedSignal, closedSignal
}

// serveEndpoints starts accepting on every endpoint socket. It runs only after the whole set plus
// control.sock is bound.
func (bound *channel) serveEndpoints(ctx context.Context, config Config, logger *log.Logger) {
	for index, listener := range bound.endpoints {
		go func(index int, listener net.Listener) {
			endpoint := config.Endpoints[index]
			for {
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					defer connection.Close()
					// The sidecar shares the host network namespace, so this reaches the host's own
					// loopback. The configured host is passed to the resolver exactly as written, so
					// localhost resolves as it would for host Codex.
					target, err := config.dial(ctx, endpoint)
					if err != nil {
						logger.Printf("relay dial %s failed: %v", endpoint, err)
						return
					}
					defer target.Close()
					logger.Printf("relay connected to %s", endpoint)
					copyBothWays(connection, target)
					logger.Printf("relay closed a connection to %s", endpoint)
				}()
			}
		}(index, listener)
	}
}

// copyBothWays moves bytes until each side closes, propagating a half-close rather than tearing the
// peer down, so a request/response exchange completes in both directions. It never interprets what
// it copies.
func copyBothWays(first, second net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(first, second)
		closeWrite(first)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(second, first)
		closeWrite(second)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func closeWrite(connection net.Conn) {
	if half, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	}
}
