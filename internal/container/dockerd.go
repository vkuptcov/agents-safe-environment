package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	dockerReadyPollInterval = 100 * time.Millisecond
	maximumDiagnosticBytes  = 16 * 1024
)

// daemonProcess is the running container-local dockerd process controlled by Supervisor.
type daemonProcess interface {
	Signal(os.Signal) error
	Kill() error
	Wait() error
}

// daemonProcessStarter starts the container-local dockerd process.
type daemonProcessStarter interface {
	Start(string, []string, io.Writer, io.Writer) (daemonProcess, error)
}

// execDaemonProcessStarter is the production daemonProcessStarter backed by os/exec.
type execDaemonProcessStarter struct{}

func (execDaemonProcessStarter) Start(
	name string,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
) (daemonProcess, error) {
	command := exec.Command(name, arguments...)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &execDaemonProcess{command: command}, nil
}

// execDaemonProcess adapts os.Process operations needed for bounded dockerd shutdown.
type execDaemonProcess struct {
	command *exec.Cmd
}

func (process *execDaemonProcess) Signal(processSignal os.Signal) error {
	return process.command.Process.Signal(processSignal)
}

func (process *execDaemonProcess) Kill() error {
	return process.command.Process.Kill()
}

func (process *execDaemonProcess) Wait() error {
	return process.command.Wait()
}

// dockerDaemon owns the one Wait call for dockerd. Closing done makes the
// result safe for both the supervisor and bounded shutdown to observe.
type dockerDaemon struct {
	process daemonProcess
	done    chan struct{}

	mutex   sync.Mutex
	waitErr error
}

func newDockerDaemon(dockerd daemonProcess) *dockerDaemon {
	daemon := &dockerDaemon{process: dockerd, done: make(chan struct{})}
	go func() {
		err := dockerd.Wait()
		daemon.mutex.Lock()
		daemon.waitErr = err
		daemon.mutex.Unlock()
		close(daemon.done)
	}()
	return daemon
}

func startDockerDaemon(
	ctx context.Context,
	config Config,
	paths containerPaths,
	starter daemonProcessStarter,
	ping func(context.Context, string) error,
	logger *log.Logger,
) (*dockerDaemon, error) {
	if err := os.MkdirAll(paths.dockerRunDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("create Docker runtime directory: %w", err)
	}
	if err := os.MkdirAll(paths.dockerDataRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create Docker data root: %w", err)
	}
	logFile, err := os.OpenFile(paths.dockerdLog, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open dockerd log: %w", err)
	}

	arguments := dockerDaemonArguments(paths)
	process, startErr := starter.Start(paths.dockerdBinary, arguments, logFile, logFile)
	closeErr := logFile.Close()
	if startErr != nil {
		return nil, fmt.Errorf("start dockerd: %w", startErr)
	}
	if closeErr != nil {
		_ = process.Kill()
		_ = process.Wait()
		return nil, fmt.Errorf("close parent dockerd log: %w", closeErr)
	}
	daemon := newDockerDaemon(process)
	logger.Printf("nested Docker daemon started")

	if err := waitForDockerReady(ctx, config.DockerReadyTimeout, paths.dockerSocket, daemon, ping); err != nil {
		_ = daemon.Stop(config.DockerShutdownTimeout)
		return nil, fmt.Errorf("%w%s", err, daemonDiagnosticSuffix(paths.dockerdLog))
	}
	if err := os.Chown(paths.dockerSocket, config.HostUID, config.HostGID); err != nil {
		_ = daemon.Stop(config.DockerShutdownTimeout)
		return nil, fmt.Errorf("set nested Docker socket owner: %w", err)
	}
	if err := os.Chmod(paths.dockerSocket, 0o600); err != nil {
		_ = daemon.Stop(config.DockerShutdownTimeout)
		return nil, fmt.Errorf("set nested Docker socket mode: %w", err)
	}
	logger.Printf("nested Docker daemon is ready")
	return daemon, nil
}

func dockerDaemonArguments(paths containerPaths) []string {
	return []string{
		"--add-runtime=crun=" + paths.crunBinary,
		"--data-root=" + paths.dockerDataRoot,
		"--default-runtime=crun",
		"--host=unix://" + paths.dockerSocket,
	}
}

func waitForDockerReady(
	ctx context.Context,
	timeout time.Duration,
	socketPath string,
	daemon *dockerDaemon,
	ping func(context.Context, string) error,
) error {
	readyContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(dockerReadyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		if err := ping(readyContext, socketPath); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-daemon.done:
			return fmt.Errorf("dockerd exited before readiness: %v", daemon.Err())
		case <-readyContext.Done():
			return fmt.Errorf("dockerd readiness at %q: %w (last error: %v)",
				socketPath, readyContext.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func pingDockerDaemon(ctx context.Context, socketPath string) error {
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "OK" {
		return fmt.Errorf("Docker _ping returned %s with body %q", response.Status, body)
	}
	return nil
}

func (daemon *dockerDaemon) Err() error {
	<-daemon.done
	daemon.mutex.Lock()
	defer daemon.mutex.Unlock()
	return daemon.waitErr
}

func (daemon *dockerDaemon) Stop(timeout time.Duration) error {
	select {
	case <-daemon.done:
		return daemon.Err()
	default:
	}
	if err := daemon.process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal dockerd: %w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-daemon.done:
		if err := daemon.Err(); err != nil {
			return fmt.Errorf("wait for dockerd shutdown: %w", err)
		}
		return nil
	case <-timer.C:
		if err := daemon.process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill dockerd after %s: %w", timeout, err)
		}
		<-daemon.done
		return fmt.Errorf("dockerd did not stop within %s", timeout)
	}
}

func daemonDiagnosticSuffix(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil || len(contents) == 0 {
		return ""
	}
	if len(contents) > maximumDiagnosticBytes {
		contents = contents[len(contents)-maximumDiagnosticBytes:]
	}
	return "\ndockerd diagnostics:\n" + strings.TrimSpace(string(contents))
}
