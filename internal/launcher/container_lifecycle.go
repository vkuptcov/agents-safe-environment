package launcher

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

const (
	sysboxRuntime        = "sysbox-runc"
	managedLabel         = "codex-safe.managed"
	projectPathLabel     = "codex-safe.project-path"
	hostUIDLabel         = "codex-safe.host-uid"
	managerProtocolLabel = "codex-safe.manager-protocol"
	codexHomeLabel       = "codex-safe.codex-home"
	personalSkillsLabel  = "codex-safe.personal-skills"
	managedLabelValue    = "true"

	containerStateTimeout   = 20 * time.Second
	containerPollInterval   = 50 * time.Millisecond
	containerCreateAttempts = 5
)

func (docker *DockerLauncher) acquireProjectContainer(
	ctx context.Context,
	cli *dockercli.Client,
	plan launchplan.Plan,
	image string,
	containerName string,
	resolution userMountResolution,
) (string, UserMounts, error) {
	inspection, found, err := docker.inspectProjectContainer(ctx, cli, containerName)
	if err != nil {
		return "", UserMounts{}, err
	}
	if found {
		if err := docker.validateProjectContainer(inspection, plan.ProjectRoot); err != nil {
			return "", UserMounts{}, err
		}
		if inspection.State.Running {
			if err := docker.validateResolvedRunningUserMounts(inspection, plan.ProjectRoot, resolution); err != nil {
				return "", UserMounts{}, err
			}
			return inspection.ID, resolution.mounts, nil
		}
		containerID, err := docker.waitForReusableOrReleased(
			ctx, cli, plan.ProjectRoot, containerName, resolution,
		)
		if err != nil {
			return "", UserMounts{}, err
		}
		if containerID != "" {
			return containerID, resolution.mounts, nil
		}
	}

	if err := docker.preflight(ctx, cli, image); err != nil {
		return "", UserMounts{}, err
	}
	userMounts, err := docker.materializeUserMounts(plan, resolution)
	if err != nil {
		return "", UserMounts{}, err
	}
	for attempt := 0; attempt < containerCreateAttempts; attempt++ {
		containerID, conflict, err := docker.createProjectContainer(
			ctx, cli, plan, image, containerName, userMounts,
		)
		if err != nil {
			return "", UserMounts{}, err
		}
		if !conflict {
			return containerID, userMounts, nil
		}
		containerID, err = docker.waitForReusableOrReleased(
			ctx,
			cli,
			plan.ProjectRoot,
			containerName,
			userMountResolution{mounts: userMounts},
		)
		if err != nil {
			return "", UserMounts{}, err
		}
		if containerID != "" {
			return containerID, userMounts, nil
		}
		if err := waitForContainerPoll(ctx); err != nil {
			return "", UserMounts{}, err
		}
	}
	return "", UserMounts{}, fmt.Errorf("container name %q was not released after a concurrent create", containerName)
}

func waitForContainerPoll(ctx context.Context) error {
	timer := time.NewTimer(containerPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (docker *DockerLauncher) createProjectContainer(
	ctx context.Context,
	cli *dockercli.Client,
	plan launchplan.Plan,
	image string,
	containerName string,
	userMounts UserMounts,
) (string, bool, error) {
	request, err := buildDockerCreateRequest(
		plan,
		image,
		containerName,
		docker.HostUID,
		docker.HostGID,
		docker.HostUser,
		docker.HostGroup,
		docker.HostHome,
		docker.HostGitConfig,
		userMounts,
	)
	if err != nil {
		return "", false, err
	}
	return cli.Create(ctx, request)
}

func (docker *DockerLauncher) inspectProjectContainer(
	ctx context.Context,
	cli *dockercli.Client,
	containerName string,
) (dockercli.ContainerInspection, bool, error) {
	return cli.Inspect(ctx, containerName)
}

// validateProjectContainer checks labels that identify the deterministic container name's owner.
func (docker *DockerLauncher) validateProjectContainer(
	inspection dockercli.ContainerInspection,
	projectRoot string,
) error {
	ownership := []struct{ name, want string }{
		{managedLabel, managedLabelValue},
		{projectPathLabel, projectRoot},
		{hostUIDLabel, strconv.Itoa(docker.HostUID)},
		{managerProtocolLabel, session.ProtocolVersion},
	}
	for _, label := range ownership {
		if got := inspection.Config.Labels[label.name]; got != label.want {
			return fmt.Errorf(
				"container %s has label %s=%q, expected %q; refusing deterministic-name reuse",
				inspection.ID,
				label.name,
				got,
				label.want,
			)
		}
	}
	return nil
}

// validateRunningUserMounts verifies immutable user-mount labels before reusing a running container.
func (docker *DockerLauncher) validateRunningUserMounts(
	inspection dockercli.ContainerInspection,
	projectRoot string,
	userMounts UserMounts,
) error {
	userMountLabels := []struct{ name, want string }{
		{codexHomeLabel, userMounts.codexHomeLabel()},
		{personalSkillsLabel, userMounts.personalSkillsLabel()},
	}
	for _, label := range userMountLabels {
		if got := inspection.Config.Labels[label.name]; got != label.want {
			return &userMountMismatchError{
				projectRoot: projectRoot,
				label:       label.name,
				running:     got,
				requested:   label.want,
			}
		}
	}
	return nil
}

func (docker *DockerLauncher) validateResolvedRunningUserMounts(
	inspection dockercli.ContainerInspection,
	projectRoot string,
	resolution userMountResolution,
) error {
	if resolution.missingCodexHome != "" {
		return fmt.Errorf(
			"a managed session for worktree %q is already running without a usable host Codex home; "+
				"finish the active session before creating and mounting %q",
			projectRoot,
			resolution.missingCodexHome,
		)
	}
	return docker.validateRunningUserMounts(inspection, projectRoot, resolution.mounts)
}

// userMountMismatchError reports immutable user mounts that differ from an active container.
type userMountMismatchError struct {
	projectRoot string
	label       string
	running     string
	requested   string
}

func (err *userMountMismatchError) Error() string {
	return fmt.Sprintf(
		"a managed session for worktree %q is already running with %s=%q, but this launch resolved "+
			"%q; finish the active session before retrying, then relaunch",
		err.projectRoot,
		err.label,
		err.running,
		err.requested,
	)
}

func (docker *DockerLauncher) waitForReusableOrReleased(
	ctx context.Context,
	cli *dockercli.Client,
	projectRoot string,
	containerName string,
	resolution userMountResolution,
) (string, error) {
	waitContext, cancel := context.WithTimeout(ctx, containerStateTimeout)
	defer cancel()
	ticker := time.NewTicker(containerPollInterval)
	defer ticker.Stop()
	for {
		inspection, found, err := docker.inspectProjectContainer(waitContext, cli, containerName)
		if err != nil {
			return "", err
		}
		if !found {
			return "", nil
		}
		if err := docker.validateProjectContainer(inspection, projectRoot); err != nil {
			return "", err
		}
		if inspection.State.Running {
			if err := docker.validateResolvedRunningUserMounts(inspection, projectRoot, resolution); err != nil {
				return "", err
			}
			return inspection.ID, nil
		}
		select {
		case <-waitContext.Done():
			return "", fmt.Errorf(
				"wait for managed container %q state %q: %w",
				containerName,
				inspection.State.Status,
				waitContext.Err(),
			)
		case <-ticker.C:
		}
	}
}

func (docker *DockerLauncher) containerStoppedAfterExec(
	ctx context.Context,
	cli *dockercli.Client,
	plan launchplan.Plan,
	containerName string,
	userMounts UserMounts,
) (bool, error) {
	inspection, found, err := docker.inspectProjectContainer(ctx, cli, containerName)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	if err := docker.validateProjectContainer(inspection, plan.ProjectRoot); err != nil {
		return false, err
	}
	if inspection.State.Running {
		if err := docker.validateRunningUserMounts(inspection, plan.ProjectRoot, userMounts); err != nil {
			return false, err
		}
		return false, nil
	}
	containerID, err := docker.waitForReusableOrReleased(
		ctx,
		cli,
		plan.ProjectRoot,
		containerName,
		userMountResolution{mounts: userMounts},
	)
	return containerID == "" && err == nil, err
}

func (docker *DockerLauncher) preflight(ctx context.Context, cli *dockercli.Client, image string) error {
	return cli.Preflight(ctx, sysboxRuntime, image)
}
