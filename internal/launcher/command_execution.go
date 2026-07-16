package launcher

import (
	"context"
	"errors"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

func (docker *DockerLauncher) execProjectCommand(
	ctx context.Context,
	cli *dockercli.Client,
	plan launchplan.Plan,
	command []string,
	containerID string,
	codexHomePresent bool,
) error {
	request, err := buildDockerExecRequest(
		plan,
		command,
		containerID,
		docker.HostUID,
		docker.HostGID,
		docker.HostHome,
		docker.AllocateTTY,
		codexHomePresent,
	)
	if err != nil {
		return err
	}
	if err := cli.Exec(
		ctx,
		request,
		docker.Stdin,
		docker.Stdout,
		docker.Stderr,
	); err != nil {
		return err
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
