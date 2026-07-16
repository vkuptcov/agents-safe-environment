package launcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

func buildDockerCreateRequest(
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
) (dockercli.CreateRequest, error) {
	if strings.TrimSpace(image) == "" {
		return dockercli.CreateRequest{}, errors.New("container image is required")
	}
	if err := validateSessionName(containerName); err != nil {
		return dockercli.CreateRequest{}, err
	}
	if hostUID < 0 || hostGID < 0 {
		return dockercli.CreateRequest{}, fmt.Errorf("invalid host identity %d:%d", hostUID, hostGID)
	}
	if err := validateAccountName("host user", hostUser); err != nil {
		return dockercli.CreateRequest{}, err
	}
	if err := validateAccountName("host group", hostGroup); err != nil {
		return dockercli.CreateRequest{}, err
	}
	if hostHome == "/" {
		return dockercli.CreateRequest{}, errors.New("host home directory cannot be the filesystem root")
	}
	if userMounts.CodexHome == "" {
		return dockercli.CreateRequest{}, errors.New("resolved Codex home is required")
	}

	mounts := make([]dockercli.Mount, 0, len(plan.Mounts)+3)
	if hostGitConfig != "" {
		mounts = append(mounts, dockercli.Mount{
			Source:   hostGitConfig,
			Target:   filepath.Join(hostHome, ".gitconfig"),
			ReadOnly: true,
		})
	}
	for _, mount := range plan.Mounts {
		mounts = append(mounts, dockercli.Mount(mount))
	}
	for _, mount := range userMountBindMounts(hostHome, userMounts) {
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
			{Key: hostUIDLabel, Value: strconv.Itoa(hostUID)},
			{Key: managerProtocolLabel, Value: session.ProtocolVersion},
			{Key: codexHomeLabel, Value: userMounts.codexHomeLabel()},
			{Key: personalSkillsLabel, Value: userMounts.personalSkillsLabel()},
		},
		Environment: []dockercli.KeyValue{
			{Key: "CODEX_SAFE_HOST_UID", Value: strconv.Itoa(hostUID)},
			{Key: "CODEX_SAFE_HOST_GID", Value: strconv.Itoa(hostGID)},
			{Key: "CODEX_SAFE_HOST_USER", Value: hostUser},
			{Key: "CODEX_SAFE_HOST_GROUP", Value: hostGroup},
			{Key: "CODEX_SAFE_HOST_HOME", Value: hostHome},
		},
		Mounts: mounts,
	}, nil
}

func buildDockerExecRequest(
	plan launchplan.Plan,
	command []string,
	containerID string,
	hostUID int,
	hostGID int,
	hostHome string,
	tty bool,
	codexHomePresent bool,
) (dockercli.ExecRequest, error) {
	if len(command) == 0 {
		return dockercli.ExecRequest{}, errors.New("command is required")
	}
	if hostUID < 0 || hostGID < 0 {
		return dockercli.ExecRequest{}, fmt.Errorf("invalid host identity %d:%d", hostUID, hostGID)
	}
	if hostHome == "/" {
		return dockercli.ExecRequest{}, errors.New("host home directory cannot be the filesystem root")
	}

	environment := []dockercli.KeyValue{{Key: "HOME", Value: hostHome}}
	if codexHomePresent {
		environment = append(environment, dockercli.KeyValue{
			Key:   "CODEX_HOME",
			Value: filepath.Join(hostHome, ".codex"),
		})
	}
	wrappedCommand := append([]string{"codex-safe-session", "run", "--"}, command...)
	return dockercli.ExecRequest{
		ContainerID: containerID,
		User:        strconv.Itoa(hostUID) + ":" + strconv.Itoa(hostGID),
		WorkingDir:  plan.WorkingDir,
		Environment: environment,
		Command:     wrappedCommand,
		Interactive: true,
		AllocateTTY: tty,
	}, nil
}

// userMountBindMounts returns the shared user-specific bind mounts for a launch.
func userMountBindMounts(hostHome string, userMounts UserMounts) []launchplan.BindMount {
	mounts := make([]launchplan.BindMount, 0, 2)
	if userMounts.CodexHomePresent() {
		mounts = append(mounts, launchplan.BindMount{
			Source: userMounts.CodexHome,
			Target: filepath.Join(hostHome, ".codex"),
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
