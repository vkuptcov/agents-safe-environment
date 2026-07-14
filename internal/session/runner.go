package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	connectionRetryInterval = 25 * time.Millisecond
	registrationACKTimeout  = 250 * time.Millisecond
	commandExitWait         = 5 * time.Second
)

// RunnerConfig describes one foreground command registered with the manager.
type RunnerConfig struct {
	// SocketPath is the container-local manager socket.
	SocketPath string
	// StartupTimeout bounds socket discovery and manager acknowledgement.
	StartupTimeout time.Duration
	// Command is the untouched executable and argv received after `run --`.
	Command []string
	// Stdin is attached directly to the command. Nil attaches no input.
	Stdin io.Reader
	// Stdout receives the command's standard output. Nil discards output.
	Stdout io.Writer
	// Stderr receives the command's standard error. Nil discards diagnostics.
	Stderr io.Writer
	// Environment replaces the inherited environment when non-nil.
	Environment []string
	// WorkingDirectory replaces the inherited working directory when non-empty.
	WorkingDirectory string
}

// CommandExitError retains a child-process failure together with the exact
// shell-compatible status the codex-safe-session process must return.
type CommandExitError struct {
	err               error
	exitCode          int
	wrapperDiagnostic bool
}

func (err *CommandExitError) Error() string {
	return err.err.Error()
}

// Unwrap exposes the underlying os/exec error.
func (err *CommandExitError) Unwrap() error {
	return err.err
}

// ExitCode returns the status the wrapper must return to Docker exec.
func (err *CommandExitError) ExitCode() int {
	return err.exitCode
}

// WrapperDiagnostic reports whether the failure happened before a child could
// write its own diagnostic to stderr.
func (err *CommandExitError) WrapperDiagnostic() bool {
	return err.wrapperDiagnostic
}

// RunCommand registers and runs one direct child. The manager connection stays
// open until the child has been waited for on every successful start path.
func RunCommand(ctx context.Context, config RunnerConfig) error {
	if len(config.Command) == 0 {
		return errors.New("session command is required")
	}
	if config.SocketPath == "" {
		return errors.New("session socket path is required")
	}
	if config.StartupTimeout <= 0 {
		return fmt.Errorf("session startup timeout must be positive, got %s", config.StartupTimeout)
	}

	registration, err := connectRegistered(ctx, config.SocketPath, config.StartupTimeout)
	if err != nil {
		return err
	}
	defer registration.Close()

	command := exec.CommandContext(ctx, config.Command[0], config.Command[1:]...)
	command.Stdin = config.Stdin
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr
	command.Env = config.Environment
	command.Dir = config.WorkingDirectory
	command.WaitDelay = commandExitWait
	command.Cancel = func() error {
		return signalProcess(command.Process, syscall.SIGTERM)
	}

	if err := command.Start(); err != nil {
		return commandStartError(err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	childDone := make(chan struct{})
	go forwardSignals(command.Process, signals, childDone)

	waitErr := command.Wait()
	close(childDone)
	signal.Stop(signals)
	if waitErr == nil {
		return nil
	}
	return commandWaitError(waitErr)
}

func connectRegistered(ctx context.Context, socketPath string, timeout time.Duration) (net.Conn, error) {
	startupContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		if err := rejectCommittedShutdown(socketPath); err != nil {
			return nil, err
		}
		connection, err := (&net.Dialer{}).DialContext(startupContext, "unix", socketPath)
		if err == nil {
			if markerErr := rejectCommittedShutdown(socketPath); markerErr != nil {
				_ = connection.Close()
				return nil, markerErr
			}
			ackDeadline := time.Now().Add(registrationACKTimeout)
			if startupDeadline, ok := startupContext.Deadline(); ok && startupDeadline.Before(ackDeadline) {
				ackDeadline = startupDeadline
			}
			_ = connection.SetReadDeadline(ackDeadline)
			acknowledgement := []byte{0}
			_, err = io.ReadFull(connection, acknowledgement)
			if err == nil && acknowledgement[0] == Acknowledgement {
				_ = connection.SetReadDeadline(time.Time{})
				return connection, nil
			}
			if err == nil {
				err = fmt.Errorf("manager sent unknown acknowledgement %d", acknowledgement[0])
			}
			_ = connection.Close()
		}
		lastErr = err
		if markerErr := rejectCommittedShutdown(socketPath); markerErr != nil {
			return nil, markerErr
		}

		timer := time.NewTimer(connectionRetryInterval)
		select {
		case <-startupContext.Done():
			timer.Stop()
			return nil, fmt.Errorf("register session command at %q: %w (last error: %v)",
				socketPath, startupContext.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func rejectCommittedShutdown(socketPath string) error {
	_, err := os.Stat(stoppingMarkerPath(socketPath))
	if err == nil {
		return fmt.Errorf("register session command at %q: manager is stopping", socketPath)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect session stopping marker: %w", err)
	}
	return nil
}

func forwardSignals(process *os.Process, signals <-chan os.Signal, childDone <-chan struct{}) {
	for {
		select {
		case processSignal := <-signals:
			_ = signalProcess(process, processSignal)
		case <-childDone:
			return
		}
	}
}

func signalProcess(process *os.Process, processSignal os.Signal) error {
	if process == nil {
		return os.ErrProcessDone
	}
	err := process.Signal(processSignal)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func commandStartError(err error) error {
	exitCode := 126
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		exitCode = 127
	}
	return &CommandExitError{
		err:               fmt.Errorf("start command: %w", err),
		exitCode:          exitCode,
		wrapperDiagnostic: true,
	}
}

func commandWaitError(err error) error {
	exitCode := 1
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		exitCode = exitError.ExitCode()
		if waitStatus, ok := exitError.ProcessState.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
			exitCode = 128 + int(waitStatus.Signal())
		}
	}
	return &CommandExitError{err: fmt.Errorf("command exited: %w", err), exitCode: exitCode}
}
