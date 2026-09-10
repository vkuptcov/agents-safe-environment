package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

const (
	sysboxRuntime        = "sysbox-runc"
	managedLabel         = "agents-safe.managed"
	projectPathLabel     = "agents-safe.project-path"
	hostUIDLabel         = "agents-safe.host-uid"
	managerProtocolLabel = "agents-safe.manager-protocol"
	codexHomeLabel       = "agents-safe.codex-home"
	claudeHomeLabel      = "agents-safe.claude-home"
	claudeConfigLabel    = "agents-safe.claude-config"
	personalSkillsLabel  = "agents-safe.personal-skills"
	goBuildCacheLabel    = "agents-safe.go-build-cache"
	goModulesCacheLabel  = "agents-safe.go-modules-cache"
	uvCacheLabel         = "agents-safe.uv-cache"
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
			if err := attempt.awaitSessionReady(ctx, inspection.ID); err != nil {
				return "", err
			}
			if err := attempt.reuseHostMCP(ctx, inspection); err != nil {
				return "", err
			}
			return inspection.ID, nil
		}
		var containerID string
		if inspection.HostConfig.AutoRemove {
			containerID, err = attempt.waitForReusableOrReleased(ctx)
		} else {
			containerID, err = attempt.restartContainer(ctx, inspection)
		}
		if err != nil {
			return "", err
		}
		if containerID != "" {
			if err := attempt.awaitSessionReady(ctx, containerID); err != nil {
				return "", err
			}
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
	if err := materializeWorktreeRegistry(attempt.plan.WorktreeRegistryDir); err != nil {
		return "", err
	}
	if err := materializeTmpfsTargets(attempt.plan.TmpfsMounts); err != nil {
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
			if err := attempt.awaitSessionReady(ctx, containerID); err != nil {
				return "", err
			}
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

// materializeHostDirectory creates one cold-create host path and fails closed on anything that is not
// already a real directory. Lstat rather than Stat is deliberate: a symlink standing where the launcher
// expects a directory would redirect the mount, so it must be rejected instead of followed.
func materializeHostDirectory(label, path string) error {
	if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create %s %q: %w", label, path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %q is not a directory", label, path)
	}
	return nil
}

// materializeWorktreeRegistry creates the source of the unconditional read-only guard only after
// active-container reuse has been ruled out. Git discovery guarantees that the common Git directory
// already exists, so a single-directory create also fails closed when the expected topology changes.
func materializeWorktreeRegistry(registry string) error {
	if registry == "" {
		return nil
	}
	return materializeHostDirectory("protected worktree registry", registry)
}

// materializeTmpfsTargets creates only heuristic-selected mountpoints and only on the cold-create path.
// The empty directory remains on the host, while Docker and privileged bootstrap cover it with session tmpfs.
func materializeTmpfsTargets(mounts []launchplan.TmpfsMount) error {
	for _, mount := range mounts {
		if !mount.CreateTarget {
			continue
		}
		if err := materializeHostDirectory("proactive tmpfs target", mount.Target); err != nil {
			return err
		}
	}
	return nil
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
		attempt.keepContainer,
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
// It is a pure predicate that polling callers re-run freely: --force-exec tolerates a mismatch here, while
// the one-time adoption warning and skipped host-MCP reconciliation happen at the reuse site.
func (attempt *launchAttempt) validateRunningFingerprint(inspection dockercli.ContainerInspection) error {
	if attempt.forcedFingerprintMismatch(inspection) || !attempt.fingerprintMismatch(inspection) {
		return nil
	}
	return &launchConfigMismatchError{
		containerName: attempt.containerName,
		containerID:   inspection.ID,
		projectRoot:   attempt.plan.ProjectRoot,
		running:       inspection.Config.Labels[launchConfigLabel],
		requested:     attempt.launchFingerprint,
		persistent:    !inspection.State.Running && !inspection.HostConfig.AutoRemove,
	}
}

// fingerprintMismatch reports whether the running container's creation fingerprint differs from this launch.
func (attempt *launchAttempt) fingerprintMismatch(inspection dockercli.ContainerInspection) bool {
	return inspection.Config.Labels[launchConfigLabel] != attempt.launchFingerprint
}

// forcedFingerprintMismatch reports whether --force-exec is adopting this running container despite a mismatch.
func (attempt *launchAttempt) forcedFingerprintMismatch(inspection dockercli.ContainerInspection) bool {
	return attempt.forceExec && attempt.fingerprintMismatch(inspection)
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
		if !inspection.HostConfig.AutoRemove {
			// A stopped container without auto-removal is not Docker's removal in progress: it is a
			// persistent session that stays stopped until something starts it. Waiting would only
			// time out, so it is restarted here and handed back as a running container.
			return attempt.restartContainer(ctx, inspection)
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
	if !inspection.HostConfig.AutoRemove {
		// The persistent session shut down under the exec. The replacement acquisition restarts it.
		return true, nil
	}
	containerID, err := attempt.waitForReusableOrReleased(ctx)
	return containerID == "" && err == nil, err
}

// restartContainer starts a stopped persistent session after the same ownership and fingerprint
// checks a running one passes, and returns it as a running container for the caller's readiness
// and host-MCP reuse steps. Forwarding resources are rebuilt first, in cold-create order, because
// the session's channel bind mount must have a source before Docker starts it.
//
// The restart is driven by the container's own removal policy, not by this launch's option: a
// container created with keep_container persists until removed, and the notice names the command
// that discards it.
func (attempt *launchAttempt) restartContainer(
	ctx context.Context,
	inspection dockercli.ContainerInspection,
) (string, error) {
	if err := attempt.validateRunningFingerprint(inspection); err != nil {
		return "", err
	}
	fmt.Fprintf(
		attempt.docker.Stderr,
		"restarting persistent session container %q (ID %q); it was created with keep_container and "+
			"persists until `docker rm %s`\n",
		attempt.containerName, inspection.ID, attempt.containerName,
	)
	if err := attempt.prepareHostMCPRestart(ctx, inspection); err != nil {
		return "", err
	}
	if err := attempt.cli.Start(ctx, attempt.containerName); err != nil {
		return "", err
	}
	return inspection.ID, nil
}
