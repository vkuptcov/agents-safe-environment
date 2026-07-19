package launcher

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

const mountAbsent = "absent"

// Host identity is validated once by validateConfiguration before a launch starts, so the request
// builders below assume it and check only what is specific to the request they encode.

func (docker *DockerLauncher) buildCreateRequest(
	plan launchplan.Plan,
	image string,
	containerName string,
	forwarding hostMCPPlan,
	launchFingerprint string,
) (dockercli.CreateRequest, error) {
	if strings.TrimSpace(image) == "" {
		return dockercli.CreateRequest{}, errors.New("container image is required")
	}
	if err := validateSessionName(containerName); err != nil {
		return dockercli.CreateRequest{}, err
	}
	mounts := make([]dockercli.Mount, 0, len(plan.Mounts)+1)
	for _, mount := range plan.Mounts {
		mounts = append(mounts, dockercli.Mount(mount))
	}

	labels := []dockercli.KeyValue{
		{Key: managedLabel, Value: managedLabelValue},
		{Key: projectPathLabel, Value: plan.ProjectRoot},
		{Key: hostUIDLabel, Value: strconv.Itoa(docker.HostUID)},
		{Key: managerProtocolLabel, Value: session.ProtocolVersion},
		{Key: launchConfigLabel, Value: launchFingerprint},
		// These unhashed values are retained for operator diagnostics. Creation-time reuse compares
		// only launchConfigLabel after the ownership and protocol checks.
		{Key: codexHomeLabel, Value: mountRoleLabel(plan, projectenv.RoleCodexHome)},
		{Key: personalSkillsLabel, Value: mountRoleLabel(plan, projectenv.RolePersonalSkills)},
		{Key: hostMCPLabel, Value: forwarding.set.Label()},
	}
	environment := []dockercli.KeyValue{
		{Key: "CODEX_SAFE_HOST_UID", Value: strconv.Itoa(docker.HostUID)},
		{Key: "CODEX_SAFE_HOST_GID", Value: strconv.Itoa(docker.HostGID)},
		{Key: "CODEX_SAFE_HOST_USER", Value: docker.HostUser},
		{Key: "CODEX_SAFE_HOST_GROUP", Value: docker.HostGroup},
		{Key: "CODEX_SAFE_HOST_HOME", Value: docker.HostHome},
	}

	// An empty endpoint set is the zero-cost path: no environment variable, no mount, and no channel
	// label. A user with no local MCP servers sees the launcher behave exactly as it did before.
	if !forwarding.set.Empty() {
		encoded, err := forwarding.set.Environment()
		if err != nil {
			return dockercli.CreateRequest{}, err
		}
		environment = append(environment, dockercli.KeyValue{Key: hostMCPEnv, Value: encoded})
		mounts = append(mounts, dockercli.Mount{
			Source: forwarding.channel.Generation,
			Target: hostmcp.SessionTarget,
		})
		// Read on reuse, never compared: it locates the channel this session's sidecar serves.
		labels = append(labels, dockercli.KeyValue{
			Key:   hostMCPChannelLabel,
			Value: forwarding.channel.Generation,
		})
	}

	return dockercli.CreateRequest{
		Image:       image,
		Name:        containerName,
		Runtime:     sysboxRuntime,
		WorkingDir:  plan.WorkingDir,
		Labels:      labels,
		Environment: environment,
		Mounts:      mounts,
	}, nil
}

func (docker *DockerLauncher) buildExecRequest(
	plan launchplan.Plan,
	command []string,
	containerID string,
) (dockercli.ExecRequest, error) {
	if len(command) == 0 {
		return dockercli.ExecRequest{}, errors.New("command is required")
	}

	environment := []dockercli.KeyValue{{Key: "HOME", Value: docker.HostHome}}
	if mount, found := plan.MountForRole(projectenv.RoleCodexHome); found {
		environment = append(environment, dockercli.KeyValue{
			Key:   "CODEX_HOME",
			Value: mount.Target,
		})
	}
	wrappedCommand := append([]string{"codex-safe-session", "run", "--"}, command...)
	return dockercli.ExecRequest{
		ContainerID: containerID,
		User:        strconv.Itoa(docker.HostUID) + ":" + strconv.Itoa(docker.HostGID),
		WorkingDir:  plan.WorkingDir,
		Environment: environment,
		Command:     wrappedCommand,
		Interactive: true,
		AllocateTTY: docker.AllocateTTY,
	}, nil
}

func mountRoleLabel(plan launchplan.Plan, role string) string {
	mount, found := plan.MountForRole(role)
	if !found {
		return mountAbsent
	}
	return mount.Source
}

// containerCodexHome is the container-local Codex home: both the Codex-home mount target and the
// CODEX_HOME value the wrapped command sees. One definition keeps the two from drifting.
func containerCodexHome(hostHome string) string {
	return filepath.Join(hostHome, ".codex")
}
