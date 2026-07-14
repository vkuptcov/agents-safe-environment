package session

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
	"time"
)

const (
	runtimeDirectoryMode = 0o700
	socketMode           = 0o600
)

// ManagerConfig describes the filesystem identity and lifetime policy of one
// container-local session manager.
type ManagerConfig struct {
	// SocketPath is the absolute path wrappers connect to inside the container.
	SocketPath string
	// SocketUID owns both the socket and its private parent directory.
	SocketUID int
	// SocketGID owns both the socket and its private parent directory.
	SocketGID int
	// IdleTimeout is the single timeout used at startup and after the final
	// command disconnects.
	IdleTimeout time.Duration
	// Log receives lifecycle diagnostics. A nil logger discards them.
	Log *log.Logger
}

// Manager counts acknowledged wrapper connections and stops after its single
// idle timeout elapses with no active connection.
type Manager struct {
	config ManagerConfig
}

// NewManager validates config without changing the filesystem.
func NewManager(config ManagerConfig) (*Manager, error) {
	if !filepath.IsAbs(config.SocketPath) {
		return nil, fmt.Errorf("session socket path %q is not absolute", config.SocketPath)
	}
	if filepath.Clean(config.SocketPath) != config.SocketPath {
		return nil, fmt.Errorf("session socket path %q is not canonical", config.SocketPath)
	}
	if config.SocketUID < 0 || config.SocketGID < 0 {
		return nil, fmt.Errorf("invalid session socket identity %d:%d", config.SocketUID, config.SocketGID)
	}
	if config.IdleTimeout <= 0 {
		return nil, fmt.Errorf("session idle timeout must be positive, got %s", config.IdleTimeout)
	}
	if config.Log == nil {
		config.Log = log.New(io.Discard, "", 0)
	}
	return &Manager{config: config}, nil
}

// Serve listens until the manager commits idle shutdown, the context is
// cancelled, or the listener fails. It removes the socket before returning.
func (manager *Manager) Serve(ctx context.Context) error {
	listener, err := manager.listen()
	if err != nil {
		return err
	}

	state := newManagerState(manager.config, listener)
	state.startIdleTimer()
	manager.config.Log.Printf("session manager listening on %s", manager.config.SocketPath)

	go func() {
		select {
		case <-ctx.Done():
			state.shutdown(ctx.Err())
		case <-state.done:
		}
	}()

	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if state.isStopping() {
				break
			}
			state.shutdown(fmt.Errorf("accept session connection: %w", acceptErr))
			break
		}
		state.register(connection)
	}

	state.connections.Wait()
	if err := os.Remove(manager.config.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		state.setError(fmt.Errorf("remove session socket: %w", err))
	}
	manager.config.Log.Printf("session manager stopped")
	return state.err()
}

func (manager *Manager) listen() (net.Listener, error) {
	runtimeDirectory := filepath.Dir(manager.config.SocketPath)
	if err := os.MkdirAll(runtimeDirectory, runtimeDirectoryMode); err != nil {
		return nil, fmt.Errorf("create session runtime directory: %w", err)
	}
	if err := os.Chown(runtimeDirectory, manager.config.SocketUID, manager.config.SocketGID); err != nil {
		return nil, fmt.Errorf("set session runtime directory owner: %w", err)
	}
	if err := os.Chmod(runtimeDirectory, runtimeDirectoryMode); err != nil {
		return nil, fmt.Errorf("set session runtime directory mode: %w", err)
	}

	if err := ensureSocketPathAvailable(manager.config.SocketPath); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", manager.config.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on session socket: %w", err)
	}
	if err := os.Chown(manager.config.SocketPath, manager.config.SocketUID, manager.config.SocketGID); err != nil {
		_ = listener.Close()
		_ = os.Remove(manager.config.SocketPath)
		return nil, fmt.Errorf("set session socket owner: %w", err)
	}
	if err := os.Chmod(manager.config.SocketPath, socketMode); err != nil {
		_ = listener.Close()
		_ = os.Remove(manager.config.SocketPath)
		return nil, fmt.Errorf("set session socket mode: %w", err)
	}
	return listener, nil
}

func ensureSocketPathAvailable(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing session socket: %w", err)
	}
	return fmt.Errorf("session socket path %q already exists", path)
}

// managerState serializes registration, timer transitions, and committed
// shutdown. Connections are kept in a map so cancellation can release all
// wrapper processes without leaking reader goroutines.
type managerState struct {
	config   ManagerConfig
	listener net.Listener

	mutex       sync.Mutex
	active      map[net.Conn]struct{}
	idleTimer   *time.Timer
	stopping    bool
	shutdownErr error
	done        chan struct{}
	connections sync.WaitGroup
}

func newManagerState(config ManagerConfig, listener net.Listener) *managerState {
	return &managerState{
		config:   config,
		listener: listener,
		active:   make(map[net.Conn]struct{}),
		done:     make(chan struct{}),
	}
}

func (state *managerState) startIdleTimer() {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.resetIdleTimerLocked()
}

func (state *managerState) register(connection net.Conn) {
	state.mutex.Lock()
	if state.stopping {
		state.mutex.Unlock()
		_ = connection.Close()
		return
	}
	if len(state.active) == 0 && state.idleTimer != nil {
		state.idleTimer.Stop()
	}
	state.active[connection] = struct{}{}
	active := len(state.active)
	state.connections.Add(1)
	state.mutex.Unlock()

	state.config.Log.Printf("session command registered: active=%d", active)
	if _, err := connection.Write([]byte{Acknowledgement}); err != nil {
		state.unregister(connection)
		state.connections.Done()
		return
	}

	go func() {
		defer state.connections.Done()
		_, _ = io.Copy(io.Discard, connection)
		state.unregister(connection)
	}()
}

func (state *managerState) unregister(connection net.Conn) {
	_ = connection.Close()

	state.mutex.Lock()
	if _, found := state.active[connection]; !found {
		state.mutex.Unlock()
		return
	}
	delete(state.active, connection)
	active := len(state.active)
	if active == 0 && !state.stopping {
		state.resetIdleTimerLocked()
	}
	state.mutex.Unlock()
	state.config.Log.Printf("session command unregistered: active=%d", active)
}

func (state *managerState) resetIdleTimerLocked() {
	if state.idleTimer != nil {
		state.idleTimer.Stop()
	}
	state.idleTimer = time.AfterFunc(state.config.IdleTimeout, state.shutdownIfIdle)
}

func (state *managerState) shutdown(err error) {
	state.mutex.Lock()
	if state.stopping {
		if state.shutdownErr == nil && err != nil {
			state.shutdownErr = err
		}
		state.mutex.Unlock()
		return
	}
	connections := state.commitShutdownLocked(err)
	state.mutex.Unlock()

	state.closeForShutdown(connections)
}

func (state *managerState) shutdownIfIdle() {
	state.mutex.Lock()
	if state.stopping || len(state.active) != 0 {
		state.mutex.Unlock()
		return
	}
	connections := state.commitShutdownLocked(nil)
	state.mutex.Unlock()

	state.closeForShutdown(connections)
}

func (state *managerState) commitShutdownLocked(err error) []net.Conn {
	state.stopping = true
	state.shutdownErr = err
	if state.idleTimer != nil {
		state.idleTimer.Stop()
	}
	connections := make([]net.Conn, 0, len(state.active))
	for connection := range state.active {
		connections = append(connections, connection)
	}
	close(state.done)
	return connections
}

func (state *managerState) closeForShutdown(connections []net.Conn) {
	_ = state.listener.Close()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (state *managerState) isStopping() bool {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	return state.stopping
}

func (state *managerState) setError(err error) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.shutdownErr == nil {
		state.shutdownErr = err
	}
}

func (state *managerState) err() error {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	return state.shutdownErr
}
