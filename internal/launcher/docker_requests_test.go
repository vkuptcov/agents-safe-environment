package launcher

import (
	"fmt"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

// runArgsFor encodes the docker run arguments a launcher would issue, so request-building tests can
// assert on the wire form.
func runArgsFor(
	docker *DockerLauncher,
	plan launchplan.Plan,
	image string,
	containerName string,
	userMounts UserMounts,
) ([]string, error) {
	request, err := docker.buildCreateRequest(plan, image, containerName, userMounts)
	if err != nil {
		return nil, err
	}
	return dockercli.BuildCreateArgs(request)
}

// execArgsFor encodes the docker exec arguments a launcher would issue.
func execArgsFor(
	docker *DockerLauncher,
	plan launchplan.Plan,
	command []string,
	containerID string,
	userMounts UserMounts,
) ([]string, error) {
	request, err := docker.buildExecRequest(plan, command, containerID, userMounts)
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
