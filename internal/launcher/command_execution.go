package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

const sessionReadinessTimeout = session.DefaultStartupTimeout

func (attempt *launchAttempt) execCommand(
	ctx context.Context,
	command []string,
	containerID string,
) error {
	docker := attempt.docker
	request, err := docker.buildExecRequest(attempt.plan, command, containerID)
	if err != nil {
		return err
	}
	return attempt.cli.Exec(ctx, request, docker.Stdin, docker.Stdout, docker.Stderr)
}

// awaitSessionReady waits as root until serve has finished account bootstrap and published the
// manager socket. The first user-owned exec must not start earlier: usermod cannot reconcile an
// account that already owns that exec process.
func (attempt *launchAttempt) awaitSessionReady(ctx context.Context, containerID string) error {
	readyContext, cancel := context.WithTimeout(ctx, sessionReadinessTimeout)
	defer cancel()
	request, err := attempt.docker.buildReadinessRequest(attempt.plan, containerID)
	if err != nil {
		return err
	}
	if err := attempt.cli.Exec(readyContext, request, nil, io.Discard, attempt.docker.Stderr); err != nil {
		return fmt.Errorf("wait for managed Sysbox session readiness: %w", err)
	}
	return nil
}

func isRetryableExecError(err error) bool {
	var commandError interface{ CommandStderr() string }
	if !errors.As(err, &commandError) {
		return false
	}
	message := strings.ToLower(commandError.CommandStderr())
	if dockercli.ExitCode(err) == 125 {
		return strings.Contains(message, "codex-safe-session: register session command")
	}
	if dockercli.ExitCode(err) != 1 || !strings.Contains(message, "error response from daemon:") {
		return false
	}
	return strings.Contains(message, "is not running") ||
		strings.Contains(message, "no such container") ||
		strings.Contains(message, "container is restarting")
}
