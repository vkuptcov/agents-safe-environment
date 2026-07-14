package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

func TestSupervisorStopsDockerdAfterManagerIdleExit(t *testing.T) {
	harness := newSupervisorHarness(t, &fakeSessionManager{serve: func(context.Context) error { return nil }})
	harness.process.finishOnSignal = true

	if err := harness.supervisor.Serve(context.Background()); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(harness.process.recordedSignals()) != 1 {
		t.Fatalf("dockerd signals = %#v, want one SIGTERM", harness.process.recordedSignals())
	}
	if harness.managerConfig.SocketUID != harness.supervisor.config.HostUID ||
		harness.managerConfig.SocketGID != harness.supervisor.config.HostGID {
		t.Fatalf("manager socket owner = %d:%d", harness.managerConfig.SocketUID, harness.managerConfig.SocketGID)
	}
	if harness.managerConfig.SocketPath != harness.supervisor.paths.sessionSocket {
		t.Fatalf("manager socket = %q", harness.managerConfig.SocketPath)
	}
}

func TestSupervisorStopsManagerWhenDockerdExits(t *testing.T) {
	managerStarted := make(chan struct{})
	manager := &fakeSessionManager{serve: func(ctx context.Context) error {
		close(managerStarted)
		<-ctx.Done()
		return ctx.Err()
	}}
	harness := newSupervisorHarness(t, manager)
	done := make(chan error, 1)
	go func() { done <- harness.supervisor.Serve(context.Background()) }()
	<-managerStarted
	harness.process.finish(errors.New("daemon crashed"))

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "dockerd exited while the session manager was active") {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after dockerd exit")
	}
}

func TestSupervisorStopsBothComponentsOnContextCancellation(t *testing.T) {
	managerStarted := make(chan struct{})
	manager := &fakeSessionManager{serve: func(ctx context.Context) error {
		close(managerStarted)
		<-ctx.Done()
		return ctx.Err()
	}}
	harness := newSupervisorHarness(t, manager)
	harness.process.finishOnSignal = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- harness.supervisor.Serve(ctx) }()
	<-managerStarted
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop after context cancellation")
	}
	if len(harness.process.recordedSignals()) != 1 {
		t.Fatalf("dockerd signals = %#v, want one SIGTERM", harness.process.recordedSignals())
	}
}

func TestSupervisorRequiresRoot(t *testing.T) {
	harness := newSupervisorHarness(t, &fakeSessionManager{})
	harness.supervisor.effectiveUID = func() int { return 1000 }
	if err := harness.supervisor.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("Serve() error = %v, want root requirement", err)
	}
}

type supervisorHarness struct {
	supervisor    *Supervisor
	process       *fakeProcess
	managerConfig session.ManagerConfig
}

func newSupervisorHarness(t *testing.T, manager sessionManager) *supervisorHarness {
	t.Helper()
	root := t.TempDir()
	config := testConfig(t)
	config.HostUID = os.Getuid()
	config.HostGID = os.Getgid()
	config.HostHome = filepath.Join(root, "home", "alex")
	config.DockerReadyTimeout = time.Second
	config.DockerShutdownTimeout = time.Second
	supervisor, err := NewSupervisor(config, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewSupervisor() error = %v", err)
	}
	paths := testRuntimePaths(root)
	paths.bashRCSource = filepath.Join(root, "image-bashrc")
	paths.sudoersFile = filepath.Join(root, "sudoers.d", "codex-safe-host")
	if err := os.WriteFile(paths.bashRCSource, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write Bash source: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.sudoersFile), 0o755); err != nil {
		t.Fatalf("create sudoers directory: %v", err)
	}

	process := newFakeProcess()
	harness := &supervisorHarness{supervisor: supervisor, process: process}
	supervisor.paths = paths
	supervisor.commands = bootstrapCommandRunner{config: config}
	supervisor.processes = &fakeProcessStarter{process: process}
	supervisor.ping = func(_ context.Context, socketPath string) error {
		return os.WriteFile(socketPath, nil, 0o600)
	}
	supervisor.newManager = func(config session.ManagerConfig) (sessionManager, error) {
		harness.managerConfig = config
		return manager, nil
	}
	supervisor.effectiveUID = func() int { return 0 }
	return harness
}

type fakeSessionManager struct {
	serve func(context.Context) error
}

func (manager *fakeSessionManager) Serve(ctx context.Context) error {
	if manager.serve == nil {
		return nil
	}
	return manager.serve(ctx)
}

type bootstrapCommandRunner struct {
	config Config
}

func (runner bootstrapCommandRunner) CombinedOutput(
	_ context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	if name == "getent" && len(arguments) == 2 {
		switch arguments[0] + ":" + arguments[1] {
		case "group:" + strconv.Itoa(runner.config.HostGID), "group:" + runner.config.HostGroup:
			return []byte(fmt.Sprintf("%s:x:%d:\n", runner.config.HostGroup, runner.config.HostGID)), nil
		case "passwd:" + strconv.Itoa(runner.config.HostUID), "passwd:" + runner.config.HostUser:
			return []byte(fmt.Sprintf(
				"%s:x:%d:%d::%s:/bin/bash\n",
				runner.config.HostUser,
				runner.config.HostUID,
				runner.config.HostGID,
				runner.config.HostHome,
			)), nil
		}
	}
	if name == "usermod" || name == "visudo" {
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected bootstrap command %s", commandKey(name, arguments...))
}
