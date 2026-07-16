package launcher

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

// Host identity is validated once by validateConfiguration before a launch starts, so the request
// builders below assume it and check only what is specific to the request they encode.

func (docker *DockerLauncher) buildCreateRequest(
	plan launchplan.Plan,
	image string,
	containerName string,
	userMounts UserMounts,
) (dockercli.CreateRequest, error) {
	if strings.TrimSpace(image) == "" {
		return dockercli.CreateRequest{}, errors.New("container image is required")
	}
	if err := validateSessionName(containerName); err != nil {
		return dockercli.CreateRequest{}, err
	}
	if userMounts.CodexHome == "" {
		return dockercli.CreateRequest{}, errors.New("resolved Codex home is required")
	}

	mounts := make([]dockercli.Mount, 0, len(plan.Mounts)+3)
	if docker.HostGitConfig != "" {
		mounts = append(mounts, dockercli.Mount{
			Source:   docker.HostGitConfig,
			Target:   filepath.Join(docker.HostHome, ".gitconfig"),
			ReadOnly: true,
		})
	}
	for _, mount := range plan.Mounts {
		mounts = append(mounts, dockercli.Mount(mount))
	}
	for _, mount := range userMountBindMounts(docker.HostHome, userMounts) {
		mounts = append(mounts, dockercli.Mount(mount))
	}
	return dockercli.CreateRequest{
		Image:      image,
		Name:       containerName,
		Runtime:    sysboxRuntime,
		WorkingDir: plan.WorkingDir,
		Labels: []dockercli.KeyValue{
			{Key: managedLabel, Value: managedLabelValue},
			{Key: projectPathLabel, Value: plan.ProjectRoot},
			{Key: hostUIDLabel, Value: strconv.Itoa(docker.HostUID)},
			{Key: managerProtocolLabel, Value: session.ProtocolVersion},
			{Key: codexHomeLabel, Value: userMounts.codexHomeLabel()},
			{Key: personalSkillsLabel, Value: userMounts.personalSkillsLabel()},
		},
		Environment: []dockercli.KeyValue{
			{Key: "CODEX_SAFE_HOST_UID", Value: strconv.Itoa(docker.HostUID)},
			{Key: "CODEX_SAFE_HOST_GID", Value: strconv.Itoa(docker.HostGID)},
			{Key: "CODEX_SAFE_HOST_USER", Value: docker.HostUser},
			{Key: "CODEX_SAFE_HOST_GROUP", Value: docker.HostGroup},
			{Key: "CODEX_SAFE_HOST_HOME", Value: docker.HostHome},
		},
		Mounts: mounts,
	}, nil
}

func (docker *DockerLauncher) buildExecRequest(
	plan launchplan.Plan,
	command []string,
	containerID string,
	userMounts UserMounts,
) (dockercli.ExecRequest, error) {
	if len(command) == 0 {
		return dockercli.ExecRequest{}, errors.New("command is required")
	}

	environment := []dockercli.KeyValue{{Key: "HOME", Value: docker.HostHome}}
	if userMounts.CodexHomePresent() {
		environment = append(environment, dockercli.KeyValue{
			Key:   "CODEX_HOME",
			Value: containerCodexHome(docker.HostHome),
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

// containerCodexHome is the container-local Codex home: both the Codex-home mount target and the
// CODEX_HOME value the wrapped command sees. One definition keeps the two from drifting.
func containerCodexHome(hostHome string) string {
	return filepath.Join(hostHome, ".codex")
}

// userMountBindMounts returns the shared user-specific bind mounts for a launch.
func userMountBindMounts(hostHome string, userMounts UserMounts) []launchplan.BindMount {
	mounts := make([]launchplan.BindMount, 0, 2)
	if userMounts.CodexHomePresent() {
		mounts = append(mounts, launchplan.BindMount{
			Source: userMounts.CodexHome,
			Target: containerCodexHome(hostHome),
		})
	}
	if userMounts.SkillsPresent() {
		mounts = append(mounts, launchplan.BindMount{
			Source:   userMounts.PersonalSkills,
			Target:   filepath.Join(hostHome, ".agents", "skills"),
			ReadOnly: true,
		})
	}
	return mounts
}
