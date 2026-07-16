package launcher

import (
	"context"
	"errors"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
)

func (attempt *launchAttempt) execCommand(
	ctx context.Context,
	command []string,
	containerID string,
	userMounts UserMounts,
) error {
	docker := attempt.docker
	request, err := docker.buildExecRequest(attempt.plan, command, containerID, userMounts)
	if err != nil {
		return err
	}
	return attempt.cli.Exec(ctx, request, docker.Stdin, docker.Stdout, docker.Stderr)
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
