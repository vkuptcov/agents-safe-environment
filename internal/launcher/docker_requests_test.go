package launcher

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

func TestCreateRequestUsesOnlyResolvedPhysicalMounts(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Mounts, dockerMounts(plan.Mounts)) {
		t.Fatalf("mounts = %#v, want resolved plan %#v", request.Mounts, plan.Mounts)
	}
	for _, label := range request.Labels {
		if label.Key == codexHomeLabel && label.Value != "/home/developer/.codex" {
			t.Fatalf("Codex label = %q, want resolved source", label.Value)
		}
	}
}

func dockerMounts(mounts []launchplan.BindMount) []dockercli.Mount {
	result := make([]dockercli.Mount, 0, len(mounts))
	for _, mount := range mounts {
		result = append(result, dockercli.Mount{Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly})
	}
	return result
}

func TestExecRequestUsesResolvedCodexTarget(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.Provenance[3].Mount.Target = "/container/codex"
	plan.Mounts[3].Target = "/container/codex"
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildExecRequest(
		plan, []string{"true"}, strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "CODEX_HOME=/container/codex"; !containsKeyValue(request.Environment, want) {
		t.Fatalf("environment = %#v, want %q", request.Environment, want)
	}
}

func containsKeyValue(values []dockercli.KeyValue, want string) bool {
	for _, value := range values {
		if value.Key+"="+value.Value == want {
			return true
		}
	}
	return false
}

// runArgsFor encodes the docker run arguments a launcher would issue, so request-building tests can
// assert on the wire form.
func runArgsFor(
	docker *DockerLauncher,
	plan launchplan.Plan,
	image string,
	containerName string,
) ([]string, error) {
	request, err := docker.buildCreateRequest(plan, image, containerName, hostMCPPlan{})
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
) ([]string, error) {
	request, err := docker.buildExecRequest(plan, command, containerID)
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
