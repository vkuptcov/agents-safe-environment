package dockercli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Runner executes the host Docker CLI and is replaceable in focused tests.
type Runner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
	Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error
}

// Client performs typed operations through the host Docker CLI.
type Client struct {
	binary string
	runner Runner
}

// New creates a Docker CLI client.
func New(binary string, runner Runner) *Client {
	return &Client{binary: binary, runner: runner}
}

// NewProcessRunner creates the production subprocess runner.
func NewProcessRunner() Runner {
	return processRunner{}
}

func (client *Client) combinedOutput(ctx context.Context, args ...string) ([]byte, error) {
	return client.runner.CombinedOutput(ctx, client.binary, args...)
}

func (client *Client) run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return client.runner.Run(ctx, client.binary, args, stdin, stdout, stderr)
}

type processRunner struct{}

func (processRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (processRunner) Run(
	ctx context.Context,
	name string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	if stderr == nil {
		stderr = io.Discard
	}
	capturedStderr := &tailBuffer{limit: 64 * 1024}
	command.Stderr = io.MultiWriter(stderr, capturedStderr)
	if err := command.Run(); err != nil {
		return &commandError{err: err, stderr: capturedStderr.String()}
	}
	return nil
}

type commandError struct {
	err    error
	stderr string
}

func (err *commandError) Error() string {
	return err.err.Error()
}

func (err *commandError) Unwrap() error {
	return err.err
}

func (err *commandError) ExitCode() int {
	return ExitCode(err.err)
}

func (err *commandError) CommandStderr() string {
	return err.stderr
}

func commandFailure(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, message)
}

// ExitCode returns an error's subprocess exit code or -1 when none is available.
func ExitCode(err error) int {
	type exitError interface{ ExitCode() int }
	var processError exitError
	if errors.As(err, &processError) {
		return processError.ExitCode()
	}
	return -1
}

// tailBuffer retains only the newest bytes written so retry diagnostics stay bounded.
type tailBuffer struct {
	limit int
	data  []byte
}

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if originalLength >= buffer.limit {
		buffer.data = append(buffer.data[:0], data[originalLength-buffer.limit:]...)
		return originalLength, nil
	}
	overflow := len(buffer.data) + originalLength - buffer.limit
	if overflow > 0 {
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:len(buffer.data)-overflow]
	}
	buffer.data = append(buffer.data, data...)
	return originalLength, nil
}

func (buffer *tailBuffer) String() string {
	return strings.TrimSpace(string(buffer.data))
}

var _ error = (*commandError)(nil)
var _ interface{ ExitCode() int } = (*commandError)(nil)
var _ interface{ CommandStderr() string } = (*commandError)(nil)
