package dockercli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// runCleanupStopTimeout bounds how long RunAttached waits for Docker to stop an owned maintenance
// container after its own client process was killed by context cancellation. It is short: the
// container runs no interactive session and cancellation already means the caller stopped waiting.
const runCleanupStopTimeout = 5 * time.Second

// RunResult is the outcome of one attached maintenance-container run that started successfully.
type RunResult struct {
	// ExitCode is the child container's exit status, preserved exactly as Docker reported it.
	ExitCode int
}

// RunAttached runs one container in the foreground with the caller's stdin/stdout/stderr attached,
// streaming output directly rather than buffering it. It is transport plumbing only: it does not know
// about update or publish policy, only how to start, observe, and clean up one deterministically named
// container.
//
// Conflict is true when the deterministic name is already in use by another container, matching
// Create's contract so a caller can report "an update is already in progress" without a second
// container ever starting.
//
// If ctx is canceled while the container is running, the local `docker run` process is killed, but the
// container itself keeps running on the daemon: Go's context cancellation sends the client process a
// kill signal, not a graceful `docker stop`, and cannot rely on Docker's signal-proxy forwarding once
// the client is already dead. RunAttached detects that case and stops its own deterministically named
// container, using a fresh context so cleanup is not itself cut short by the same cancellation.
func (client *Client) RunAttached(
	ctx context.Context,
	request CreateRequest,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) (result RunResult, conflict bool, err error) {
	arguments, err := BuildRunAttachedArgs(request)
	if err != nil {
		return RunResult{}, false, err
	}

	runErr := client.run(ctx, arguments, stdin, stdout, stderr)
	if runErr == nil {
		return RunResult{ExitCode: 0}, false, nil
	}
	if isRunNameConflict(runErr) {
		return RunResult{}, true, nil
	}
	if ctx.Err() != nil {
		if cleanupErr := client.Stop(context.Background(), request.Name, runCleanupStopTimeout); cleanupErr != nil {
			return RunResult{}, false, fmt.Errorf(
				"run maintenance container %q: %w (cleanup after cancellation also failed: %v)",
				request.Name, ctx.Err(), cleanupErr,
			)
		}
		return RunResult{}, false, ctx.Err()
	}

	code := ExitCode(runErr)
	if code < 0 {
		return RunResult{}, false, commandFailure(fmt.Sprintf("run maintenance container %q", request.Name), nil, runErr)
	}
	return RunResult{ExitCode: code}, false, nil
}

// isRunNameConflict detects the same "container name already in use" failure Create recognizes, but
// from a wrapped run error rather than combined command output: an attached run streams its output
// live, so only the bounded stderr tail carried by the run error is available after the fact.
func isRunNameConflict(err error) bool {
	if ExitCode(err) != 125 {
		return false
	}
	var diagnostic interface{ CommandStderr() string }
	if !errors.As(err, &diagnostic) {
		return false
	}
	return isNameConflictMessage(diagnostic.CommandStderr())
}
