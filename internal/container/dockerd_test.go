package container

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestStartDockerDaemonUsesFixedArgvAndSocketOwnership(t *testing.T) {
	root := t.TempDir()
	paths := testContainerPaths(root)
	config := testConfig(t)
	config.HostUID = os.Getuid()
	config.HostGID = os.Getgid()
	config.DockerReadyTimeout = time.Second
	process := newFakeDaemonProcess()
	starter := &fakeDaemonProcessStarter{process: process}
	pingCalls := 0
	ping := func(_ context.Context, socketPath string) error {
		pingCalls++
		if pingCalls == 1 {
			return errors.New("not ready")
		}
		return os.WriteFile(socketPath, nil, 0o666)
	}

	daemon, err := startDockerDaemon(
		context.Background(),
		config,
		paths,
		starter,
		ping,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatalf("startDockerDaemon() error = %v", err)
	}
	wantArguments := []string{
		"--add-runtime=crun=" + paths.crunBinary,
		"--data-root=" + paths.dockerDataRoot,
		"--default-runtime=crun",
		"--host=unix://" + paths.dockerSocket,
	}
	if starter.name != paths.dockerdCommand || !reflect.DeepEqual(starter.arguments, wantArguments) {
		t.Fatalf("start = %q %#v, want %q %#v", starter.name, starter.arguments, paths.dockerdCommand, wantArguments)
	}
	info, err := os.Stat(paths.dockerSocket)
	if err != nil {
		t.Fatalf("stat Docker socket: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("Docker socket mode = %o, want 600", got)
	}
	process.finish(nil)
	if err := daemon.Err(); err != nil {
		t.Fatalf("daemon wait error = %v", err)
	}
}

func TestStartDockerDaemonReportsEarlyExitAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	paths := testContainerPaths(root)
	config := testConfig(t)
	config.DockerReadyTimeout = time.Second
	process := newFakeDaemonProcess()
	starter := &fakeDaemonProcessStarter{process: process, logContents: "dockerd exploded\n"}
	process.finish(errors.New("exit status 1"))

	_, err := startDockerDaemon(
		context.Background(),
		config,
		paths,
		starter,
		func(context.Context, string) error { return errors.New("connection refused") },
		log.New(io.Discard, "", 0),
	)
	if err == nil || !stringsContainAll(err.Error(), "exited before readiness", "dockerd exploded") {
		t.Fatalf("startDockerDaemon() error = %v", err)
	}
}

func TestDockerDaemonStopSendsTermAndWaits(t *testing.T) {
	process := newFakeDaemonProcess()
	process.finishOnSignal = true
	daemon := newDockerDaemon(process)
	if err := daemon.Stop(time.Second); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if signals := process.recordedSignals(); !reflect.DeepEqual(signals, []os.Signal{syscall.SIGTERM}) {
		t.Fatalf("signals = %#v, want SIGTERM", signals)
	}
}

func TestPingDockerDaemonUsesPrivateUnixSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/_ping" {
			t.Errorf("request path = %q, want /_ping", request.URL.Path)
		}
		_, _ = response.Write([]byte("OK"))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	if err := pingDockerDaemon(context.Background(), socketPath); err != nil {
		t.Fatalf("pingDockerDaemon() error = %v", err)
	}
}

type fakeDaemonProcessStarter struct {
	process     *fakeDaemonProcess
	name        string
	arguments   []string
	logContents string
}

func (starter *fakeDaemonProcessStarter) Start(
	name string,
	arguments []string,
	stdout io.Writer,
	_ io.Writer,
) (daemonProcess, error) {
	starter.name = name
	starter.arguments = append([]string{}, arguments...)
	if starter.logContents != "" {
		_, _ = io.WriteString(stdout, starter.logContents)
	}
	return starter.process, nil
}

type fakeDaemonProcess struct {
	mutex          sync.Mutex
	done           chan struct{}
	waitErr        error
	signals        []os.Signal
	finishOnce     sync.Once
	finishOnSignal bool
}

func newFakeDaemonProcess() *fakeDaemonProcess {
	return &fakeDaemonProcess{done: make(chan struct{})}
}

func (process *fakeDaemonProcess) Signal(processSignal os.Signal) error {
	process.mutex.Lock()
	process.signals = append(process.signals, processSignal)
	finish := process.finishOnSignal
	process.mutex.Unlock()
	if finish {
		process.finish(nil)
	}
	return nil
}

func (process *fakeDaemonProcess) Kill() error {
	process.finish(errors.New("killed"))
	return nil
}

func (process *fakeDaemonProcess) Wait() error {
	<-process.done
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return process.waitErr
}

func (process *fakeDaemonProcess) finish(err error) {
	process.finishOnce.Do(func() {
		process.mutex.Lock()
		process.waitErr = err
		process.mutex.Unlock()
		close(process.done)
	})
}

func (process *fakeDaemonProcess) recordedSignals() []os.Signal {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return append([]os.Signal{}, process.signals...)
}

func testContainerPaths(root string) containerPaths {
	paths := defaultContainerPaths()
	paths.dockerRunDirectory = filepath.Join(root, "run", "docker")
	paths.dockerDataRoot = filepath.Join(root, "lib", "docker")
	paths.dockerSocket = filepath.Join(root, "docker.sock")
	paths.dockerdLog = filepath.Join(root, "dockerd.log")
	paths.crunBinary = filepath.Join(root, "crun")
	paths.dockerdCommand = filepath.Join(root, "dockerd")
	paths.sessionSocket = filepath.Join(root, "run", "codex-safe", "session.sock")
	return paths
}

func stringsContainAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
