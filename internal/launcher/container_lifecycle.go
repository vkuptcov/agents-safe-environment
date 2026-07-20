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
	goBuildCacheLabel    = "codex-safe.go-build-cache"
	goModulesCacheLabel  = "codex-safe.go-modules-cache"
	managedLabelValue    = "true"

	containerStateTimeout   = 20 * time.Second
	containerPollInterval   = 50 * time.Millisecond
	containerCreateAttempts = 5

	// containerStopTimeout is how long Docker waits for a container to stop gracefully before
	// killing it. A relay sidecar has only sockets to release, so it needs no more.
	containerStopTimeout = 10 * time.Second
)

func (attempt *launchAttempt) acquireContainer(
	ctx context.Context,
) (string, error) {
	inspection, found, err := attempt.inspectOwnedContainer(ctx)
	if err != nil {
		return "", err
	}
	if found {
		if inspection.State.Running {
			if err := attempt.validateRunningFingerprint(inspection); err != nil {
				return "", err
			}
			if err := attempt.reuseHostMCP(ctx, inspection); err != nil {
				return "", err
			}
			return inspection.ID, nil
		}
		containerID, err := attempt.waitForReusableOrReleased(ctx)
		if err != nil {
			return "", err
		}
		if containerID != "" {
			if err := attempt.reuseHostMCPAfterWait(ctx); err != nil {
				return "", err
			}
			return containerID, nil
		}
	}

	if err := attempt.prepareImage(ctx); err != nil {
		return "", err
	}
	if err := attempt.resolveHostMCPImage(ctx); err != nil {
		return "", err
	}
	for count := 0; count < containerCreateAttempts; count++ {
		containerID, conflict, err := attempt.createSessionWithHostMCP(ctx)
		if err != nil {
			return "", err
		}
		if !conflict {
			return containerID, nil
		}
		// This attempt lost the session-name race. Its candidate sidecar is transient by
		// construction: stop and await only this attempt's own, and remove only its generation.
		if err := attempt.discardHostMCPCandidate(ctx); err != nil {
			return "", err
		}
		containerID, err = attempt.waitForReusableOrReleased(ctx)
		if err != nil {
			return "", err
		}
		if containerID != "" {
			if err := attempt.reuseHostMCPAfterWait(ctx); err != nil {
				return "", err
			}
			return containerID, nil
		}
		// The winner vanished before it could be adopted, so this attempt will try to create again.
		// Its previous candidate generation was just removed, so allocate a fresh one before retrying.
		if err := attempt.reallocateHostMCPCandidate(); err != nil {
			return "", err
		}
		if err := waitForContainerPoll(ctx); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf(
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
) (string, bool, error) {
	request, err := attempt.docker.buildCreateRequest(
		attempt.plan, attempt.image, attempt.containerName, attempt.hostMCP, attempt.launchFingerprint,
	)
	if err != nil {
		return "", false, err
	}
	// Both containers must come from one immutable image, so a resolved ID supersedes the reference.
	if attempt.hostMCPImageID != "" {
		request.Image = attempt.hostMCPImageID
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

// validateRunningFingerprint is the sole creation-time reuse predicate after ownership and protocol checks.
func (attempt *launchAttempt) validateRunningFingerprint(inspection dockercli.ContainerInspection) error {
	if running := inspection.Config.Labels[launchConfigLabel]; running != attempt.launchFingerprint {
		return &launchConfigMismatchError{
			projectRoot: attempt.plan.ProjectRoot,
			running:     running,
			requested:   attempt.launchFingerprint,
		}
	}
	return nil
}

func (attempt *launchAttempt) waitForReusableOrReleased(
	ctx context.Context,
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
			if err := attempt.validateRunningFingerprint(inspection); err != nil {
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
) (bool, error) {
	inspection, found, err := attempt.inspectOwnedContainer(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	if inspection.State.Running {
		if err := attempt.validateRunningFingerprint(inspection); err != nil {
			return false, err
		}
		return false, nil
	}
	containerID, err := attempt.waitForReusableOrReleased(ctx)
	return containerID == "" && err == nil, err
}
