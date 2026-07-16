package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helperEnvironment = "CODEX_SAFE_SESSION_TEST_HELPER"

func TestRunCommandPreservesArgvStreamsEnvironmentAndDirectory(t *testing.T) {
	running := startTestManager(t, time.Second)
	workingDirectory := t.TempDir()
	input := "hello from stdin"
	var output bytes.Buffer
	var stderr bytes.Buffer

	err := RunCommand(context.Background(), CommandConfig{
		SocketPath:     running.socketPath,
		StartupTimeout: time.Second,
		Command: helperCommand(
			"report",
			"value with spaces",
			"$shell;characters",
			"",
		),
		Stdin:            strings.NewReader(input),
		Stdout:           &output,
		Stderr:           &stderr,
		Environment:      helperEnvironmentValues("REPORT_VALUE=kept"),
		WorkingDirectory: workingDirectory,
	})
	if err != nil {
		t.Fatalf("RunCommand() error = %v, stderr=%q", err, stderr.String())
	}

	var report helperReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode helper report %q: %v", output.String(), err)
	}
	wantArgs := []string{"value with spaces", "$shell;characters", ""}
	if fmt.Sprint(report.Arguments) != fmt.Sprint(wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", report.Arguments, wantArgs)
	}
	if report.Input != input {
		t.Fatalf("stdin = %q, want %q", report.Input, input)
	}
	if report.Environment != "kept" {
		t.Fatalf("environment = %q, want kept", report.Environment)
	}
	if report.WorkingDirectory != workingDirectory {
		t.Fatalf("working directory = %q, want %q", report.WorkingDirectory, workingDirectory)
	}
	running.cancel()
	waitForManager(t, running, time.Second, context.Canceled)
}

func TestRunCommandReturnsExactExitStatus(t *testing.T) {
	running := startTestManager(t, time.Second)
	err := RunCommand(context.Background(), CommandConfig{
		SocketPath:     running.socketPath,
		StartupTimeout: time.Second,
		Command:        helperCommand("exit", "42"),
		Environment:    helperEnvironmentValues(),
	})
	assertCommandExitCode(t, err, 42)
}

func TestRunCommandReturnsNotFoundStatus(t *testing.T) {
	running := startTestManager(t, time.Second)
	err := RunCommand(context.Background(), CommandConfig{
		SocketPath:     running.socketPath,
		StartupTimeout: time.Second,
		Command:        []string{filepath.Join(t.TempDir(), "missing-command")},
	})
	assertCommandExitCode(t, err, 127)
}

func TestRunCommandReturnsSignalDerivedStatus(t *testing.T) {
	running := startTestManager(t, time.Second)
	err := RunCommand(context.Background(), CommandConfig{
		SocketPath:     running.socketPath,
		StartupTimeout: time.Second,
		Command:        helperCommand("terminate-self"),
		Environment:    helperEnvironmentValues(),
	})
	assertCommandExitCode(t, err, 128+int(syscall.SIGTERM))
}

func TestRunCommandHoldsRegistrationUntilChildExit(t *testing.T) {
	running := startTestManager(t, testIdleTimeout)
	reader, writer := io.Pipe()
	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- RunCommand(context.Background(), CommandConfig{
			SocketPath:     running.socketPath,
			StartupTimeout: time.Second,
			Command:        helperCommand("read-until-eof"),
			Stdin:          reader,
			Environment:    helperEnvironmentValues(),
		})
	}()

	assertManagerRunning(t, running, 2*testIdleTimeout)
	if err := writer.Close(); err != nil {
		t.Fatalf("close helper stdin: %v", err)
	}
	select {
	case err := <-runnerDone:
		if err != nil {
			t.Fatalf("RunCommand() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunCommand() did not return after child exit")
	}
	waitForManager(t, running, 5*testIdleTimeout, nil)
}

func TestRunCommandForwardsTerminationSignal(t *testing.T) {
	running := startTestManager(t, time.Second)
	wrapper := exec.Command(
		os.Args[0],
		"-test.run=^TestRunnerHelperProcess$",
		"--",
		"signal-wrapper",
		running.socketPath,
	)
	wrapper.Env = helperEnvironmentValues()
	stdout, err := wrapper.StdoutPipe()
	if err != nil {
		t.Fatalf("create wrapper stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	wrapper.Stderr = &stderr
	if err := wrapper.Start(); err != nil {
		t.Fatalf("start wrapper helper: %v", err)
	}

	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(stdout, ready); err != nil {
		t.Fatalf("wait for child readiness: %v, stderr=%q", err, stderr.String())
	}
	if string(ready) != "ready\n" {
		t.Fatalf("readiness = %q, want ready", ready)
	}
	if err := wrapper.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal wrapper: %v", err)
	}
	assertProcessExitCode(t, wrapper.Wait(), 23)
}

func TestRunCommandRequiresManagerAcknowledgement(t *testing.T) {
	started := time.Now()
	err := RunCommand(context.Background(), CommandConfig{
		SocketPath:     filepath.Join(t.TempDir(), "missing.sock"),
		StartupTimeout: 60 * time.Millisecond,
		Command:        helperCommand("exit", "0"),
		Environment:    helperEnvironmentValues(),
	})
	if err == nil || !strings.Contains(err.Error(), "register session command") {
		t.Fatalf("RunCommand() error = %v, want registration failure", err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond || elapsed > time.Second {
		t.Fatalf("registration timeout elapsed = %s", elapsed)
	}
}

func TestRunCommandRejectsCommittedManagerShutdownWithoutStartingChild(t *testing.T) {
	runtimeDirectory := t.TempDir()
	socketPath := filepath.Join(runtimeDirectory, "session.sock")
	if err := os.WriteFile(stoppingMarkerPath(socketPath), nil, 0o600); err != nil {
		t.Fatalf("create stopping marker: %v", err)
	}
	started := time.Now()
	err := RunCommand(context.Background(), CommandConfig{
		SocketPath:     socketPath,
		StartupTimeout: time.Second,
		Command:        helperCommand("exit", "99"),
		Environment:    helperEnvironmentValues(),
	})
	if err == nil || !strings.Contains(err.Error(), "manager is stopping") {
		t.Fatalf("RunCommand() error = %v, want committed shutdown", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("committed shutdown detection took %s", elapsed)
	}
}

func TestRunCommandDoesNotWaitOnUnacknowledgedShutdownSocket(t *testing.T) {
	runtimeDirectory := t.TempDir()
	socketPath := filepath.Join(runtimeDirectory, "session.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()
	go func() {
		connection := <-accepted
		defer connection.Close()
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(stoppingMarkerPath(socketPath), nil, 0o600)
	}()

	started := time.Now()
	err = RunCommand(context.Background(), CommandConfig{
		SocketPath:     socketPath,
		StartupTimeout: 5 * time.Second,
		Command:        helperCommand("exit", "99"),
		Environment:    helperEnvironmentValues(),
	})
	if err == nil || !strings.Contains(err.Error(), "manager is stopping") {
		t.Fatalf("RunCommand() error = %v, want committed shutdown", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("unacknowledged shutdown socket blocked for %s", elapsed)
	}
}

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvironment) != "1" {
		return
	}
	arguments := helperArguments(os.Args)
	if len(arguments) == 0 {
		os.Exit(90)
	}

	switch arguments[0] {
	case "report":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(91)
		}
		workingDirectory, err := os.Getwd()
		if err != nil {
			os.Exit(92)
		}
		report := helperReport{
			Arguments:        arguments[1:],
			Input:            string(input),
			Environment:      os.Getenv("REPORT_VALUE"),
			WorkingDirectory: workingDirectory,
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			os.Exit(93)
		}
		os.Exit(0)
	case "exit":
		if len(arguments) != 2 {
			os.Exit(94)
		}
		var code int
		if _, err := fmt.Sscan(arguments[1], &code); err != nil {
			os.Exit(95)
		}
		os.Exit(code)
	case "read-until-eof":
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "terminate-self":
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			os.Exit(99)
		}
		time.Sleep(time.Second)
		os.Exit(100)
	case "signal-wrapper":
		if len(arguments) != 2 {
			os.Exit(96)
		}
		err := RunCommand(context.Background(), CommandConfig{
			SocketPath:     arguments[1],
			StartupTimeout: time.Second,
			Command:        helperCommand("wait-signal"),
			Stdout:         os.Stdout,
			Stderr:         os.Stderr,
			Environment:    helperEnvironmentValues(),
		})
		os.Exit(commandErrorExitCode(err))
	case "wait-signal":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		fmt.Println("ready")
		if <-signals == syscall.SIGTERM {
			os.Exit(23)
		}
		os.Exit(97)
	default:
		os.Exit(98)
	}
}

type helperReport struct {
	Arguments        []string `json:"arguments"`
	Input            string   `json:"input"`
	Environment      string   `json:"environment"`
	WorkingDirectory string   `json:"working_directory"`
}

func helperCommand(arguments ...string) []string {
	return append([]string{os.Args[0], "-test.run=^TestRunnerHelperProcess$", "--"}, arguments...)
}

func helperEnvironmentValues(values ...string) []string {
	environment := append([]string{}, os.Environ()...)
	environment = append(environment, helperEnvironment+"=1")
	return append(environment, values...)
}

func helperArguments(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}

func assertCommandExitCode(t *testing.T, err error, want int) {
	t.Helper()
	var exitError interface{ ExitCode() int }
	if !errors.As(err, &exitError) {
		t.Fatalf("error %v has no ExitCode()", err)
	}
	if got := exitError.ExitCode(); got != want {
		t.Fatalf("exit code = %d, want %d", got, want)
	}
}

func assertProcessExitCode(t *testing.T, err error, want int) {
	t.Helper()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("process error = %v, want exit status %d", err, want)
	}
	if got := exitError.ExitCode(); got != want {
		t.Fatalf("process exit code = %d, want %d", got, want)
	}
}

func commandErrorExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return 1
}
