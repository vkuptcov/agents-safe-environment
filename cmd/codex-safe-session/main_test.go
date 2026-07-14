package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

func TestRunCLIRequiresStrictRunSeparator(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no subcommand"},
		{name: "unknown", args: []string{"unknown"}},
		{name: "run without separator", args: []string{"run", "bash"}},
		{name: "run without command", args: []string{"run", "--"}},
		{name: "serve arguments", args: []string{"serve", "extra"}},
		{name: "help arguments", args: []string{"help", "extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := runCLI(context.Background(), test.args, nil, io.Discard, &stderr, unusedApplication()); got != 2 {
				t.Fatalf("exit code = %d, want 2; stderr=%q", got, stderr.String())
			}
		})
	}
}

func TestRunCLIHelp(t *testing.T) {
	var stdout bytes.Buffer
	if got := runCLI(
		context.Background(),
		[]string{"--help"},
		nil,
		&stdout,
		io.Discard,
		unusedApplication(),
	); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if !strings.Contains(stdout.String(), "codex-safe-session run -- COMMAND") {
		t.Fatalf("help = %q", stdout.String())
	}
}

func TestRunCLIForwardsRunConfiguration(t *testing.T) {
	stdin := strings.NewReader("input")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var captured session.RunnerConfig
	app := unusedApplication()
	app.run = func(_ context.Context, config session.RunnerConfig) error {
		captured = config
		return nil
	}

	arguments := []string{"bash", "-c", "printf value"}
	if got := runCLI(
		context.Background(),
		append([]string{"run", "--"}, arguments...),
		stdin,
		&stdout,
		&stderr,
		app,
	); got != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", got, stderr.String())
	}
	if !reflect.DeepEqual(captured.Command, arguments) {
		t.Fatalf("command = %#v, want %#v", captured.Command, arguments)
	}
	if captured.SocketPath != session.DefaultSocketPath || captured.StartupTimeout != session.DefaultStartupTimeout {
		t.Fatalf("session contract = %q/%s", captured.SocketPath, captured.StartupTimeout)
	}
	if captured.Stdin != stdin || captured.Stdout != &stdout || captured.Stderr != &stderr {
		t.Fatal("run streams were not forwarded unchanged")
	}
}

func TestRunCLIReturnsChildExitCodeWithoutDiagnostic(t *testing.T) {
	app := unusedApplication()
	app.run = func(context.Context, session.RunnerConfig) error {
		return fakeExitError{code: 37}
	}
	var stderr bytes.Buffer
	if got := runCLI(
		context.Background(),
		[]string{"run", "--", "false"},
		nil,
		io.Discard,
		&stderr,
		app,
	); got != 37 {
		t.Fatalf("exit code = %d, want 37", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no wrapper diagnostic", stderr.String())
	}
}

func TestRunCLIReportsCommandStartFailure(t *testing.T) {
	app := unusedApplication()
	app.run = func(context.Context, session.RunnerConfig) error {
		return reportingExitError{code: 127}
	}
	var stderr bytes.Buffer
	if got := runCLI(
		context.Background(),
		[]string{"run", "--", "missing"},
		nil,
		io.Discard,
		&stderr,
		app,
	); got != 127 {
		t.Fatalf("exit code = %d, want 127", got)
	}
	if !strings.Contains(stderr.String(), "child failed") {
		t.Fatalf("stderr = %q, want start diagnostic", stderr.String())
	}
}

func TestRunCLIServeTreatsSignalCancellationAsCleanExit(t *testing.T) {
	app := unusedApplication()
	app.serve = func(context.Context, *log.Logger) error {
		return nil
	}
	if got := runCLI(context.Background(), []string{"serve"}, nil, io.Discard, io.Discard, app); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
}

func TestRunCLIReportsInfrastructureFailure(t *testing.T) {
	app := unusedApplication()
	app.run = func(context.Context, session.RunnerConfig) error {
		return errors.New("socket unavailable")
	}
	var stderr bytes.Buffer
	if got := runCLI(
		context.Background(),
		[]string{"run", "--", "true"},
		nil,
		io.Discard,
		&stderr,
		app,
	); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), "socket unavailable") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

type fakeExitError struct {
	code int
}

type reportingExitError struct {
	code int
}

func (err reportingExitError) Error() string {
	return "child failed"
}

func (err reportingExitError) ExitCode() int {
	return err.code
}

func (err reportingExitError) WrapperDiagnostic() bool {
	return true
}

func (err fakeExitError) Error() string {
	return "child failed"
}

func (err fakeExitError) ExitCode() int {
	return err.code
}

func unusedApplication() application {
	return application{
		serve: func(context.Context, *log.Logger) error {
			return errors.New("unexpected serve")
		},
		run: func(context.Context, session.RunnerConfig) error {
			return errors.New("unexpected run")
		},
	}
}
