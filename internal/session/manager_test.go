package session

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testIdleTimeout = 80 * time.Millisecond

func TestManagerExitsAfterStartupIdleTimeout(t *testing.T) {
	running := startTestManager(t, testIdleTimeout)
	waitForManager(t, running, 5*testIdleTimeout, nil)
	assertSocketRemoved(t, running.socketPath)
	_, err := os.Stat(stoppingMarkerPath(running.socketPath))
	require.NoError(t, err, "manager shutdown must leave the stopping marker")
}

func TestManagerStaysAliveUntilEveryConnectionCloses(t *testing.T) {
	running := startTestManager(t, testIdleTimeout)
	first := connectAcknowledged(t, running.socketPath)
	second := connectAcknowledged(t, running.socketPath)

	assertManagerRunning(t, running, 2*testIdleTimeout)
	require.NoError(t, first.Close(), "first wrapper connection must close")
	assertManagerRunning(t, running, 2*testIdleTimeout)
	require.NoError(t, second.Close(), "second wrapper connection must close")

	waitForManager(t, running, 5*testIdleTimeout, nil)
}

func TestManagerReconnectCancelsIdleShutdown(t *testing.T) {
	running := startTestManager(t, 4*testIdleTimeout)
	first := connectAcknowledged(t, running.socketPath)
	require.NoError(t, first.Close(), "initial wrapper connection must close")

	time.Sleep(testIdleTimeout)
	second := connectAcknowledged(t, running.socketPath)
	assertManagerRunning(t, running, 5*testIdleTimeout)
	require.NoError(t, second.Close(), "reconnected wrapper connection must close")
	waitForManager(t, running, 8*testIdleTimeout, nil)
}

func TestManagerShutdownRaceAcknowledgesOrRejectsConnection(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		running := startTestManager(t, 20*time.Millisecond)
		time.Sleep(15 * time.Millisecond)

		connection, err := net.DialTimeout("unix", running.socketPath, 20*time.Millisecond)
		if err == nil {
			_ = connection.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			ack := []byte{0}
			read, readErr := io.ReadFull(connection, ack)
			if readErr == nil {
				if read != 1 || ack[0] != Acknowledgement {
					t.Fatalf("unexpected acknowledgement %v", ack[:read])
				}
				assertManagerRunning(t, running, 30*time.Millisecond)
			}
			_ = connection.Close()
		}
		waitForManager(t, running, 200*time.Millisecond, nil)
	}
}

func TestManagerContextCancellationClosesActiveConnections(t *testing.T) {
	running := startTestManager(t, time.Second)
	connection := connectAcknowledged(t, running.socketPath)
	running.cancel()

	waitForManager(t, running, time.Second, context.Canceled)
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	buffer := []byte{0}
	read, err := connection.Read(buffer)
	require.Error(t, err, "manager cancellation must close active wrapper connections")
	require.Zero(t, read, "closed wrapper connection must not return data")
}

func TestManagerCreatesPrivateSocket(t *testing.T) {
	running := startTestManager(t, time.Second)
	t.Cleanup(running.cancel)

	directoryInfo, err := os.Stat(filepath.Dir(running.socketPath))
	require.NoError(t, err, "manager runtime directory must exist")
	require.Equal(t,
		os.FileMode(runtimeDirectoryMode),
		directoryInfo.Mode().Perm(),
		"manager runtime directory must be private",
	)
	assertOwner(t, "runtime directory", directoryInfo)
	socketInfo, err := os.Stat(running.socketPath)
	require.NoError(t, err, "manager socket must exist")
	require.Equal(t, os.FileMode(socketMode), socketInfo.Mode().Perm(), "manager socket must be private")
	assertOwner(t, "socket", socketInfo)
}

func TestManagerRejectsNonSocketAtSocketPath(t *testing.T) {
	runtimeDirectory := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimeDirectory, 0o700); err != nil {
		t.Fatalf("create runtime directory: %v", err)
	}
	socketPath := filepath.Join(runtimeDirectory, "session.sock")
	if err := os.WriteFile(socketPath, []byte("keep"), 0o600); err != nil {
		t.Fatalf("create conflicting file: %v", err)
	}
	manager, err := newTestManager(socketPath, testIdleTimeout)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Serve(context.Background()); err == nil {
		t.Fatal("Serve() succeeded with a non-socket path")
	}
	contents, err := os.ReadFile(socketPath)
	if err != nil {
		t.Fatalf("read conflicting file: %v", err)
	}
	if string(contents) != "keep" {
		t.Fatalf("conflicting file contents = %q, want keep", contents)
	}
}

type runningManager struct {
	socketPath string
	cancel     context.CancelFunc
	done       <-chan error
	stopped    <-chan struct{}
}

func startTestManager(t *testing.T, idleTimeout time.Duration) runningManager {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "runtime", "session.sock")
	manager, err := newTestManager(socketPath, idleTimeout)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		done <- manager.Serve(ctx)
		close(stopped)
	}()
	waitForSocket(t, socketPath, done)
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Errorf("manager cleanup did not stop within one second")
		}
		_ = os.Remove(stoppingMarkerPath(socketPath))
	})
	return runningManager{socketPath: socketPath, cancel: cancel, done: done, stopped: stopped}
}

func newTestManager(socketPath string, idleTimeout time.Duration) (*Manager, error) {
	return NewManager(ManagerConfig{
		SocketPath:  socketPath,
		SocketUID:   os.Getuid(),
		SocketGID:   os.Getgid(),
		IdleTimeout: idleTimeout,
		Log:         log.New(io.Discard, "", 0),
	})
}

func waitForSocket(t *testing.T, socketPath string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(socketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("manager exited before socket became ready: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session socket %q did not become ready", socketPath)
}

func connectAcknowledged(t *testing.T, socketPath string) net.Conn {
	t.Helper()
	connection, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("connect to manager: %v", err)
	}
	ack := []byte{0}
	if _, err := io.ReadFull(connection, ack); err != nil {
		_ = connection.Close()
		t.Fatalf("read acknowledgement: %v", err)
	}
	if ack[0] != Acknowledgement {
		_ = connection.Close()
		t.Fatalf("acknowledgement = %d, want %d", ack[0], Acknowledgement)
	}
	return connection
}

func assertManagerRunning(t *testing.T, running runningManager, duration time.Duration) {
	t.Helper()
	select {
	case err := <-running.done:
		t.Fatalf("manager exited while a command was registered: %v", err)
	case <-time.After(duration):
	}
}

func waitForManager(t *testing.T, running runningManager, timeout time.Duration, want error) {
	t.Helper()
	select {
	case err := <-running.done:
		if !errors.Is(err, want) {
			t.Fatalf("manager error = %v, want %v", err, want)
		}
	case <-time.After(timeout):
		t.Fatalf("manager did not exit within %s", timeout)
	}
}

func assertSocketRemoved(t *testing.T, socketPath string) {
	t.Helper()
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket still exists after manager exit: %v", err)
	}
}

func assertOwner(t *testing.T, name string, info os.FileInfo) {
	t.Helper()
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s stat has type %T, want *syscall.Stat_t", name, info.Sys())
	}
	if int(stat.Uid) != os.Getuid() || int(stat.Gid) != os.Getgid() {
		t.Fatalf("%s owner = %d:%d, want %d:%d", name, stat.Uid, stat.Gid, os.Getuid(), os.Getgid())
	}
}
