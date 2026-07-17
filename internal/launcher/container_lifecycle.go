package launcher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
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

func (attempt *launchAttempt) acquireContainer(
	ctx context.Context,
	resolution userMountResolution,
) (string, UserMounts, error) {
	inspection, found, err := attempt.inspectOwnedContainer(ctx)
	if err != nil {
		return "", UserMounts{}, err
	}
	if found {
		if inspection.State.Running {
			if err := attempt.validateResolvedRunningUserMounts(inspection, resolution); err != nil {
				return "", UserMounts{}, err
			}
			return inspection.ID, resolution.mounts, nil
		}
		containerID, err := attempt.waitForReusableOrReleased(ctx, resolution)
		if err != nil {
			return "", UserMounts{}, err
		}
		if containerID != "" {
			return containerID, resolution.mounts, nil
		}
	}

	if err := attempt.cli.Preflight(ctx, sysboxRuntime, attempt.image); err != nil {
		return "", UserMounts{}, err
	}
	userMounts, err := attempt.docker.materializeUserMounts(attempt.plan, resolution)
	if err != nil {
		return "", UserMounts{}, err
	}
	for count := 0; count < containerCreateAttempts; count++ {
		containerID, conflict, err := attempt.createContainer(ctx, userMounts)
		if err != nil {
			return "", UserMounts{}, err
		}
		if !conflict {
			return containerID, userMounts, nil
		}
		containerID, err = attempt.waitForReusableOrReleased(ctx, userMountResolution{mounts: userMounts})
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
	return "", UserMounts{}, fmt.Errorf(
		"container name %q was not released after a concurrent create", attempt.containerName,
	)
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

// createContainer creates the session container. It is the only path that does so, which is why the
// explicit-runtime invariant is enforced here: BuildCreateArgs now accepts an empty runtime so the
// relay sidecar can take the Docker default, and the launcher must never compensate for missing
// Sysbox by silently falling back to it.
func (attempt *launchAttempt) createContainer(
	ctx context.Context,
	userMounts UserMounts,
) (string, bool, error) {
	request, err := attempt.docker.buildCreateRequest(
		attempt.plan, attempt.image, attempt.containerName, userMounts,
	)
	if err != nil {
		return "", false, err
	}
	if request.Runtime == "" {
		return "", false, errors.New("session container must be created with an explicit runtime")
	}
	return attempt.cli.Create(ctx, request)
}

// inspectOwnedContainer inspects the deterministic container name and reports a container only once
// its labels prove this launch owns it, so no caller can act on a container it does not own.
func (attempt *launchAttempt) inspectOwnedContainer(
	ctx context.Context,
) (dockercli.ContainerInspection, bool, error) {
	inspection, found, err := attempt.cli.Inspect(ctx, attempt.containerName)
	if err != nil || !found {
		return dockercli.ContainerInspection{}, false, err
	}
	if err := attempt.validateOwnership(inspection); err != nil {
		return dockercli.ContainerInspection{}, false, err
	}
	return inspection, true, nil
}

// validateOwnership checks labels that identify the deterministic container name's owner.
func (attempt *launchAttempt) validateOwnership(inspection dockercli.ContainerInspection) error {
	ownership := []struct{ name, want string }{
		{managedLabel, managedLabelValue},
		{projectPathLabel, attempt.plan.ProjectRoot},
		{hostUIDLabel, strconv.Itoa(attempt.docker.HostUID)},
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
func (attempt *launchAttempt) validateRunningUserMounts(
	inspection dockercli.ContainerInspection,
	userMounts UserMounts,
) error {
	userMountLabels := []struct{ name, want string }{
		{codexHomeLabel, userMounts.codexHomeLabel()},
		{personalSkillsLabel, userMounts.personalSkillsLabel()},
	}
	for _, label := range userMountLabels {
		if got := inspection.Config.Labels[label.name]; got != label.want {
			return &userMountMismatchError{
				projectRoot: attempt.plan.ProjectRoot,
				label:       label.name,
				running:     got,
				requested:   label.want,
			}
		}
	}
	return nil
}

func (attempt *launchAttempt) validateResolvedRunningUserMounts(
	inspection dockercli.ContainerInspection,
	resolution userMountResolution,
) error {
	if resolution.missingCodexHome != "" {
		return fmt.Errorf(
			"a managed session for worktree %q is already running without a usable host Codex home; "+
				"finish the active session before creating and mounting %q",
			attempt.plan.ProjectRoot,
			resolution.missingCodexHome,
		)
	}
	return attempt.validateRunningUserMounts(inspection, resolution.mounts)
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

func (attempt *launchAttempt) waitForReusableOrReleased(
	ctx context.Context,
	resolution userMountResolution,
) (string, error) {
	waitContext, cancel := context.WithTimeout(ctx, containerStateTimeout)
	defer cancel()
	ticker := time.NewTicker(containerPollInterval)
	defer ticker.Stop()
	for {
		inspection, found, err := attempt.inspectOwnedContainer(waitContext)
		if err != nil {
			return "", err
		}
		if !found {
			return "", nil
		}
		if inspection.State.Running {
			if err := attempt.validateResolvedRunningUserMounts(inspection, resolution); err != nil {
				return "", err
			}
			return inspection.ID, nil
		}
		select {
		case <-waitContext.Done():
			return "", fmt.Errorf(
				"wait for managed container %q state %q: %w",
				attempt.containerName,
				inspection.State.Status,
				waitContext.Err(),
			)
		case <-ticker.C:
		}
	}
}

func (attempt *launchAttempt) containerStoppedAfterExec(
	ctx context.Context,
	userMounts UserMounts,
) (bool, error) {
	inspection, found, err := attempt.inspectOwnedContainer(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	if inspection.State.Running {
		if err := attempt.validateRunningUserMounts(inspection, userMounts); err != nil {
			return false, err
		}
		return false, nil
	}
	containerID, err := attempt.waitForReusableOrReleased(ctx, userMountResolution{mounts: userMounts})
	return containerID == "" && err == nil, err
}
