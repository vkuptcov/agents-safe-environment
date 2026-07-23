package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

const (
	mountAbsent            = "absent"
	tmpfsMountsEnvironment = "AGENTS_SAFE_TMPFS_MOUNTS"
)

type bootstrapTmpfsMount struct {
	Target string `json:"target"`
	Mode   string `json:"mode"`
	// Owned asks privileged bootstrap to chown the mount to the host user and remount it executable.
	// It rides the bootstrap wire but not the creation fingerprint, so it never changes session-reuse
	// identity.
	Owned bool `json:"owned,omitempty"`
}

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
		{Key: claudeHomeLabel, Value: mountRoleLabel(plan, projectenv.RoleClaudeHome)},
		{Key: claudeConfigLabel, Value: mountRoleLabel(plan, projectenv.RoleClaudeConfig)},
		{Key: personalSkillsLabel, Value: mountRoleLabel(plan, projectenv.RolePersonalSkills)},
		{Key: hostMCPLabel, Value: forwarding.set.Label()},
		{Key: goBuildCacheLabel, Value: dependencyCacheLabel(plan, projectenv.DependencyCacheGoBuild)},
		{Key: goModulesCacheLabel, Value: dependencyCacheLabel(plan, projectenv.DependencyCacheGoModules)},
		{Key: uvCacheLabel, Value: dependencyCacheLabel(plan, projectenv.DependencyCacheUV)},
	}
	environment := []dockercli.KeyValue{
		{Key: "CODEX_SAFE_HOST_UID", Value: strconv.Itoa(docker.HostUID)},
		{Key: "CODEX_SAFE_HOST_GID", Value: strconv.Itoa(docker.HostGID)},
		{Key: "CODEX_SAFE_HOST_USER", Value: docker.HostUser},
		{Key: "CODEX_SAFE_HOST_GROUP", Value: docker.HostGroup},
		{Key: "CODEX_SAFE_HOST_HOME", Value: docker.HostHome},
	}
	if len(plan.TmpfsMounts) > 0 {
		encoded, err := encodeBootstrapTmpfsMounts(plan.TmpfsMounts)
		if err != nil {
			return dockercli.CreateRequest{}, err
		}
		environment = append(environment, dockercli.KeyValue{Key: tmpfsMountsEnvironment, Value: encoded})
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
		Volumes: []dockercli.VolumeMount{
			{
				Source:   CodexInstallationVolume,
				Target:   CodexInstallationRoot,
				ReadOnly: true,
			},
			{
				Source:   ClaudeInstallationVolume,
				Target:   ClaudeInstallationRoot,
				ReadOnly: true,
			},
		},
		Tmpfs: dockerTmpfsMounts(plan.TmpfsMounts),
	}, nil
}

func encodeBootstrapTmpfsMounts(mounts []launchplan.TmpfsMount) (string, error) {
	wire := make([]bootstrapTmpfsMount, 0, len(mounts))
	for _, mount := range mounts {
		wire = append(wire, bootstrapTmpfsMount{Target: mount.Target, Mode: mount.Mode, Owned: mount.Owned})
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("encode container tmpfs mounts: %w", err)
	}
	return string(encoded), nil
}

func dockerTmpfsMounts(resolved []launchplan.TmpfsMount) []dockercli.TmpfsMount {
	mounts := make([]dockercli.TmpfsMount, 0, len(resolved))
	for _, mount := range resolved {
		// Owned marks the virtual-environment masks, which are exactly the mounts that must run code.
		mounts = append(mounts, dockercli.TmpfsMount{Target: mount.Target, Mode: mount.Mode, Exec: mount.Owned})
	}
	return mounts
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
	if mount, found := plan.MountForRole(projectenv.RoleClaudeHome); found {
		// An explicit CLAUDE_CONFIG_DIR is represented by a directory role without the separate
		// default ~/.claude.json role. Default state keeps Claude's native split paths unchanged.
		if _, defaultConfigFound := plan.MountForRole(projectenv.RoleClaudeConfig); !defaultConfigFound {
			environment = append(environment, dockercli.KeyValue{
				Key:   "CLAUDE_CONFIG_DIR",
				Value: mount.Target,
			})
		}
	}
	for _, cache := range plan.DependencyCaches {
		key, err := cache.EnvironmentKey()
		if err != nil {
			return dockercli.ExecRequest{}, err
		}
		environment = append(environment, dockercli.KeyValue{Key: key, Value: cache.Target})
	}
	wrappedCommand := append([]string{"agents-safe-session", "run", "--"}, command...)
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

// buildReadinessRequest builds the root-only cold-start barrier. It uses the container-local
// session socket as the durable signal that account bootstrap completed; it never starts a managed
// command or exposes the socket outside the container.
func (docker *DockerLauncher) buildReadinessRequest(
	plan launchplan.Plan,
	containerID string,
) (dockercli.ExecRequest, error) {
	if containerID == "" {
		return dockercli.ExecRequest{}, errors.New("container ID is required")
	}
	return dockercli.ExecRequest{
		ContainerID: containerID,
		User:        "0:0",
		WorkingDir:  plan.WorkingDir,
		Command:     []string{"agents-safe-session", "wait-ready"},
	}, nil
}

func dependencyCacheLabel(plan launchplan.Plan, kind projectenv.DependencyCacheKind) string {
	for _, cache := range plan.DependencyCaches {
		if cache.Kind == kind {
			return cache.Source
		}
	}
	return mountAbsent
}

func mountRoleLabel(plan launchplan.Plan, role projectenv.MountRole) string {
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

// containerClaudeConfigDir is the container-local target for either the default ~/.claude state
// directory or an explicit host CLAUDE_CONFIG_DIR.
func containerClaudeConfigDir(hostHome string) string {
	return filepath.Join(hostHome, ".claude")
}
