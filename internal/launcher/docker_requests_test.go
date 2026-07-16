package launcher

import (
	"fmt"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

func buildDockerRunArgsForTest(
	plan launchplan.Plan,
	image string,
	containerName string,
	hostUID int,
	hostGID int,
	hostUser string,
	hostGroup string,
	hostHome string,
	hostGitConfig string,
	userMounts UserMounts,
) ([]string, error) {
	if err := launchplan.ValidateMountPath("host home directory", hostHome); err != nil {
		return nil, err
	}
	if hostGitConfig != "" {
		if err := launchplan.ValidateMountPath("host Git config", hostGitConfig); err != nil {
			return nil, err
		}
	}
	request, err := buildDockerCreateRequest(
		plan,
		image,
		containerName,
		hostUID,
		hostGID,
		hostUser,
		hostGroup,
		hostHome,
		hostGitConfig,
		userMounts,
	)
	if err != nil {
		return nil, err
	}
	return dockercli.BuildCreateArgs(request)
}

func buildDockerExecArgsForTest(
	plan launchplan.Plan,
	command []string,
	containerID string,
	hostUID int,
	hostGID int,
	hostHome string,
	tty bool,
	codexHomePresent bool,
) ([]string, error) {
	request, err := buildDockerExecRequest(
		plan,
		command,
		containerID,
		hostUID,
		hostGID,
		hostHome,
		tty,
		codexHomePresent,
	)
	if err != nil {
		return nil, err
	}
	return dockercli.BuildExecArgs(request)
}

type testCommandError struct {
	err    error
	stderr string
}

func (err *testCommandError) Error() string {
	return fmt.Sprintf("docker command: %v", err.err)
}

func (err *testCommandError) Unwrap() error {
	return err.err
}

func (err *testCommandError) ExitCode() int {
	return dockercli.ExitCode(err.err)
}

func (err *testCommandError) CommandStderr() string {
	return err.stderr
}
