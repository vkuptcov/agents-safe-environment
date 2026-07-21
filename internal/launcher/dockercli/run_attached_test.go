package dockercli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// attachedRunner is a fake Runner whose Run streams fixed content to the caller's writers and then
// returns a canned error, while recording every CombinedOutput invocation so a test can assert
// cleanup behavior (such as a Stop call) after a run failure.
type attachedRunner struct {
	stdout, stderr string
	runErr         error

	combinedCalls [][]string
	combinedErr   error
}

func (runner *attachedRunner) CombinedOutput(ctx context.Context, _ string, args ...string) ([]byte, error) {
	runner.combinedCalls = append(runner.combinedCalls, args)
	// Honor cancellation like the real transport does, so a test can distinguish cleanup driven by a
	// live context from cleanup that (incorrectly) reused a cancelled one.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, runner.combinedErr
}

func (runner *attachedRunner) Run(
	_ context.Context, _ string, _ []string, _ io.Reader, stdout io.Writer, stderr io.Writer,
) error {
	_, _ = io.WriteString(stdout, runner.stdout)
	_, _ = io.WriteString(stderr, runner.stderr)
	return runner.runErr
}

func TestRunAttachedStreamsOutputAndReturnsZeroExitCode(t *testing.T) {
	t.Parallel()
	runner := &attachedRunner{stdout: "hello stdout", stderr: "hello stderr"}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	result, conflict, err := New("docker", runner).RunAttached(
		context.Background(), CreateRequest{Image: "image", Name: "maintenance"}, nil, stdout, stderr,
	)
	if err != nil {
		t.Fatalf("RunAttached() error = %v", err)
	}
	if conflict {
		t.Fatal("RunAttached() conflict = true on a clean run")
	}
	if result.ExitCode != 0 {
		t.Fatalf("RunAttached() ExitCode = %d, want 0", result.ExitCode)
	}
	if stdout.String() != "hello stdout" || stderr.String() != "hello stderr" {
		t.Fatalf("RunAttached() did not stream output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunAttachedPreservesNonZeroChildExitCode(t *testing.T) {
	t.Parallel()
	runner := &attachedRunner{runErr: &commandError{err: fakeExitError{code: 3}, stderr: "updater failed"}}
	result, conflict, err := New("docker", runner).RunAttached(
		context.Background(), CreateRequest{Image: "image", Name: "maintenance"}, nil, io.Discard, io.Discard,
	)
	if err != nil {
		t.Fatalf("RunAttached() error = %v, want the exit code preserved without a Go error", err)
	}
	if conflict {
		t.Fatal("RunAttached() conflict = true on an ordinary non-zero exit")
	}
	if result.ExitCode != 3 {
		t.Fatalf("RunAttached() ExitCode = %d, want 3", result.ExitCode)
	}
}

func TestRunAttachedDetectsDeterministicNameConflict(t *testing.T) {
	t.Parallel()
	runner := &attachedRunner{runErr: &commandError{
		err:    fakeExitError{code: 125},
		stderr: "Conflict. The container name \"/maintenance\" is already in use",
	}}
	result, conflict, err := New("docker", runner).RunAttached(
		context.Background(), CreateRequest{Image: "image", Name: "maintenance"}, nil, io.Discard, io.Discard,
	)
	if err != nil {
		t.Fatalf("RunAttached() error = %v", err)
	}
	if !conflict {
		t.Fatal("RunAttached() conflict = false, want true for a name-conflict failure")
	}
	if result.ExitCode != 0 {
		t.Fatalf("RunAttached() ExitCode = %d on a conflict, want 0", result.ExitCode)
	}
}

func TestRunAttachedDoesNotMistakeUnrelatedExit125ForNameConflict(t *testing.T) {
	t.Parallel()
	// Exit 125 is Docker's generic "the run command itself failed" code; only a message naming a
	// container-name conflict is one. An unrelated 125 (here a host-port clash) must fall through to the
	// preserved exit code, not be reported as "an update is already in progress".
	runner := &attachedRunner{runErr: &commandError{
		err:    fakeExitError{code: 125},
		stderr: "docker: Error response from daemon: driver failed programming external connectivity: port is already in use",
	}}
	result, conflict, err := New("docker", runner).RunAttached(
		context.Background(), CreateRequest{Image: "image", Name: "maintenance"}, nil, io.Discard, io.Discard,
	)
	if err != nil {
		t.Fatalf("RunAttached() error = %v, want the exit code preserved", err)
	}
	if conflict {
		t.Fatal("RunAttached() conflict = true for an exit-125 failure that is not a name conflict")
	}
	if result.ExitCode != 125 {
		t.Fatalf("RunAttached() ExitCode = %d, want 125 preserved", result.ExitCode)
	}
}

func TestRunAttachedReportsRunFailureWithoutExitCode(t *testing.T) {
	t.Parallel()
	// A live context plus a plain error (no recoverable exit code) is a genuine launch failure, not a
	// cancellation and not a name conflict. It must surface as an error naming the container, never as
	// a silent zero-exit success.
	runner := &attachedRunner{runErr: errors.New("exec: docker not found")}
	result, conflict, err := New("docker", runner).RunAttached(
		context.Background(), CreateRequest{Image: "image", Name: "maintenance"}, nil, io.Discard, io.Discard,
	)
	if err == nil {
		t.Fatal("RunAttached() error = nil, want a launch failure to be reported")
	}
	if conflict {
		t.Fatal("RunAttached() conflict = true on a plain run failure")
	}
	if result.ExitCode != 0 {
		t.Fatalf("RunAttached() ExitCode = %d, want 0 on a failure with no exit code", result.ExitCode)
	}
	if !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("RunAttached() error = %v, want it to name the container", err)
	}
}

func TestRunAttachedCleansUpOwnedContainerOnCancellation(t *testing.T) {
	t.Parallel()
	runner := &attachedRunner{runErr: errors.New("signal: killed")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, conflict, err := New("docker", runner).RunAttached(
		ctx, CreateRequest{Image: "image", Name: "codex-safe-codex-update-v1-1000-linux-amd64"}, nil, io.Discard, io.Discard,
	)
	if conflict {
		t.Fatal("RunAttached() conflict = true on cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunAttached() error = %v, want context.Canceled", err)
	}
	// The fake CombinedOutput fails on a cancelled context, so a clean context.Canceled (no wrapped
	// "cleanup ... also failed" suffix) proves Stop was handed a live, fresh context. A regression to
	// Stop(ctx, ...) with the cancelled context would make cleanup fail and surface here.
	if strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("RunAttached() error = %v; cleanup must run on a fresh context, not the cancelled one", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("RunAttached() ExitCode = %d on cancellation, want 0", result.ExitCode)
	}
	if len(runner.combinedCalls) != 1 {
		t.Fatalf("cleanup calls = %#v, want exactly one stop call", runner.combinedCalls)
	}
	stopArgs := runner.combinedCalls[0]
	if stopArgs[0] != "stop" || stopArgs[len(stopArgs)-1] != "codex-safe-codex-update-v1-1000-linux-amd64" {
		t.Fatalf("cleanup argv = %#v, want a stop of the owned deterministic name", stopArgs)
	}
}

func TestRunAttachedReportsCleanupFailureAfterCancellation(t *testing.T) {
	t.Parallel()
	runner := &attachedRunner{
		runErr:      errors.New("signal: killed"),
		combinedErr: fakeExitError{code: 1},
	}
	// combinedErr alone (no "no such container" text) makes Stop treat this as a real failure.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, conflict, err := New("docker", runner).RunAttached(
		ctx, CreateRequest{Image: "image", Name: "maintenance"}, nil, io.Discard, io.Discard,
	)
	if conflict {
		t.Fatal("RunAttached() conflict = true on cancellation with a cleanup failure")
	}
	if err == nil {
		t.Fatal("RunAttached() must surface a cleanup failure after cancellation")
	}
	if !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("RunAttached() error = %v, want it to name the container", err)
	}
}
