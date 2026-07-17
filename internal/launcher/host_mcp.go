package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

const (
	hostMCPLabel        = "codex-safe.host-mcp"
	hostMCPChannelLabel = "codex-safe.host-mcp-channel"
	hostMCPSidecarLabel = "codex-safe.host-mcp-sidecar"
	hostMCPImageLabel   = "codex-safe.host-mcp-image"
	hostMCPEnv          = "CODEX_SAFE_HOST_MCP"

	// sidecarInitialLeaseTimeout must exceed both the cold sidecar-create-to-first-lease bound and
	// serve's lease retry interval, or a sidecar gives up on a session that is still starting and
	// each later launcher repeats the same failed cycle.
	sidecarInitialLeaseTimeout = 60 * time.Second

	// readinessTimeout must in turn exceed the sidecar's, or the launcher can give up while a sidecar
	// that would still be leased is waiting.
	readinessTimeout = 90 * time.Second

	// sessionCreateTimeout is the launcher's half of the cold-start bound. Together with serve's own
	// 20s pre-lease deadline it makes that bound a fact rather than an assumption.
	sessionCreateTimeout = 20 * time.Second

	readinessPollInterval = 50 * time.Millisecond
)

// hostMCPPlan is one launch attempt's forwarding decision: the endpoint set it resolved and the
// channel it will use. Both are empty for a launch that forwards nothing, which is the zero-cost
// path through every step below.
type hostMCPPlan struct {
	set     hostmcp.Set
	channel hostmcp.Channel
	// candidate is true while this attempt owns the channel it allocated. It becomes false once the
	// attempt adopts a running session's channel instead, because a launcher removes only what its
	// own attempt created.
	candidate bool
	// sidecarStarted records that this attempt created and started its candidate sidecar, so a
	// launch that fails afterwards can stop it promptly instead of leaving it to its initial-lease
	// timeout.
	sidecarStarted bool
}

// removeCandidate discards a generation this attempt allocated and then did not use.
func (forwarding hostMCPPlan) removeCandidate() error {
	if !forwarding.candidate {
		return nil
	}
	return forwarding.channel.Remove()
}

// cleanupCandidate unwinds a candidate this attempt allocated but did not hand off: it stops the
// candidate sidecar it started and removes the generation directory it created.
//
// It runs on every failure after a candidate exists. Without it, a launch that fails between sidecar
// creation and handoff would leave the sidecar running until its 60-second initial-lease timeout;
// with it, the sidecar is stopped at once. It removes only this attempt's own resources and adopts
// nothing, so a launch that already adopted a running session's channel is left untouched.
func (attempt *launchAttempt) cleanupCandidate(ctx context.Context) error {
	if !attempt.hostMCP.candidate {
		return nil
	}
	var stopErr error
	if attempt.hostMCP.sidecarStarted {
		name := sidecarName(attempt.projectKey, attempt.hostMCP.channel)
		stopErr = attempt.stopSidecar(ctx, name)
	}
	return errors.Join(stopErr, attempt.hostMCP.removeCandidate())
}

// sidecarName is the relay sidecar's deterministic name.
//
// The generation makes each launch attempt's name distinct, so two attempts both create a sidecar
// and the session container's name remains the sole arbiter of creation. It also keeps successive
// sessions distinct, so a departing sidecar cannot hold the name an arriving one needs.
func sidecarName(projectKey string, channel hostmcp.Channel) string {
	return "codex-safe-mcp-" + projectKey + "-" + channel.Name
}

// planHostMCP resolves the forwarded endpoint set and allocates this attempt's candidate channel.
//
// Discovery reads the Codex home this launch already resolved, so --no-host-mcp and a launch with no
// Codex home both reduce to an empty set with no separate resolution path. An empty set allocates
// nothing: no directory, no environment variable, no mount, no relay, and no banner line.
func (attempt *launchAttempt) planHostMCP(resolution userMountResolution) error {
	if attempt.noHostMCP {
		// --no-host-mcp performs no config.toml read: discovery does not run at all.
		return nil
	}
	codexHome := resolution.mounts.CodexHome
	if !resolution.mounts.CodexHomePresent() {
		// A missing home is resolved later, if at all; either way it carries no config.toml yet.
		codexHome = ""
	}
	set, err := hostmcp.Discover(codexHome)
	if err != nil {
		return err
	}
	if set.Empty() {
		return nil
	}
	channel, err := hostmcp.NewChannel(attempt.docker.lookupEnv(), attempt.projectKey, len(set.Endpoints))
	if err != nil {
		return err
	}
	attempt.hostMCP = hostMCPPlan{set: set, channel: channel, candidate: true}
	return nil
}

// reallocateHostMCPCandidate allocates a fresh generation for the same endpoint set, after a
// previous candidate was discarded. Every session creation gets a new random generation; a
// discarded one is never reused.
func (attempt *launchAttempt) reallocateHostMCPCandidate() error {
	if attempt.hostMCP.set.Empty() {
		return nil
	}
	channel, err := hostmcp.NewChannel(
		attempt.docker.lookupEnv(), attempt.projectKey, len(attempt.hostMCP.set.Endpoints),
	)
	if err != nil {
		return err
	}
	attempt.hostMCP = hostMCPPlan{set: attempt.hostMCP.set, channel: channel, candidate: true}
	return nil
}

// resolveHostMCPImage pins this launch to one immutable image ID, so the session container and its
// sidecar cannot end up on different builds of a private protocol. It runs only on the non-empty
// path: with no endpoints there is no sidecar and no protocol, so the resolution has no purpose.
func (attempt *launchAttempt) resolveHostMCPImage(ctx context.Context) error {
	if attempt.hostMCP.set.Empty() {
		return nil
	}
	imageID, err := attempt.cli.ResolveImageID(ctx, attempt.image)
	if err != nil {
		return err
	}
	attempt.hostMCPImageID = imageID
	return nil
}

// createSessionWithHostMCP creates this attempt's sidecar and then its session container, in the
// order the design fixes: the generation directory exists, then the sidecar, then the session.
func (attempt *launchAttempt) createSessionWithHostMCP(
	ctx context.Context,
	userMounts UserMounts,
) (string, bool, error) {
	if !attempt.hostMCP.set.Empty() {
		name := sidecarName(attempt.projectKey, attempt.hostMCP.channel)
		if err := attempt.ensureSidecar(
			ctx, name, attempt.hostMCPImageID, attempt.hostMCP.channel, attempt.hostMCP.set,
		); err != nil {
			return "", false, err
		}
		// The candidate sidecar is now this attempt's to clean up if the launch fails from here.
		attempt.hostMCP.sidecarStarted = true
	}
	containerID, conflict, err := attempt.createContainer(ctx, userMounts)
	if err != nil || conflict {
		return containerID, conflict, err
	}
	if err := attempt.awaitHostMCPReady(ctx, containerID); err != nil {
		return "", false, err
	}
	return containerID, false, nil
}

// awaitHostMCPReady blocks until the channel is ready, then prints the forwarded set.
//
// A non-empty set is always printed before the command starts, because this feature widens the
// security boundary and that must never be silent.
func (attempt *launchAttempt) awaitHostMCPReady(ctx context.Context, sessionID string) error {
	if attempt.hostMCP.set.Empty() {
		return nil
	}
	name := sidecarName(attempt.projectKey, attempt.hostMCP.channel)
	if err := attempt.awaitChannelReady(ctx, attempt.hostMCP.channel, name, sessionID); err != nil {
		return err
	}
	attempt.printForwardedEndpoints()
	return nil
}

func (attempt *launchAttempt) printForwardedEndpoints() {
	for _, line := range attempt.hostMCP.set.BannerLines() {
		fmt.Fprintf(attempt.docker.Stderr, "Forwarding host MCP endpoints: %s\n", line)
	}
}

// discardHostMCPCandidate stops and awaits only this attempt's own sidecar and removes only its own
// generation, for the race loser that must keep its candidate transient. It adopts nothing: the
// winner's resources are not this attempt's to touch. It leaves the plan marked as a candidate so a
// later failure in the same launch still cleans up, but clears sidecarStarted because the sidecar is
// already gone.
func (attempt *launchAttempt) discardHostMCPCandidate(ctx context.Context) error {
	if err := attempt.cleanupCandidate(ctx); err != nil {
		return err
	}
	attempt.hostMCP.sidecarStarted = false
	return nil
}

// reuseHostMCPAfterWait re-inspects a container that became reusable while this attempt waited, and
// applies host-MCP reuse to it. It is a no-op for a launch that forwards nothing, so the empty-set
// path stays exactly as it was before this feature: no extra inspect, no new failure mode.
func (attempt *launchAttempt) reuseHostMCPAfterWait(ctx context.Context) error {
	if attempt.hostMCP.set.Empty() {
		return nil
	}
	inspection, found, err := attempt.inspectOwnedContainer(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("managed container %q vanished before host MCP reuse", attempt.containerName)
	}
	return attempt.reuseHostMCP(ctx, inspection)
}

// reuseHostMCP validates a running session's forwarding against this launch's resolution, adopts its
// channel, and recreates a sidecar that has died.
func (attempt *launchAttempt) reuseHostMCP(ctx context.Context, inspection dockercli.ContainerInspection) error {
	if err := attempt.validateRunningHostMCP(inspection, attempt.hostMCP.set); err != nil {
		return err
	}
	if attempt.hostMCP.set.Empty() {
		return nil
	}

	adopted, err := adoptedChannel(inspection)
	if err != nil {
		return err
	}
	// The candidate this attempt allocated is not the one the running session uses.
	if err := attempt.hostMCP.removeCandidate(); err != nil {
		return err
	}
	attempt.hostMCP.channel = adopted
	attempt.hostMCP.candidate = false

	// Recovery uses the running session's own image, not the reference this later launcher was given,
	// which may name a tag that has moved since.
	imageID := inspection.Image
	if imageID == "" {
		return errors.New("the running session records no image ID")
	}
	attempt.hostMCPImageID = imageID

	name := sidecarName(attempt.projectKey, adopted)
	if err := attempt.ensureSidecar(ctx, name, imageID, adopted, attempt.hostMCP.set); err != nil {
		return err
	}
	if err := attempt.awaitChannelReady(ctx, adopted, name, inspection.ID); err != nil {
		return err
	}
	attempt.printForwardedEndpoints()
	return nil
}

// buildSidecarRequest encodes the relay sidecar's create request.
//
// Every confinement here is load-bearing. The sidecar gains exactly one thing over the session
// container -- the host network namespace, which is the capability this feature exists to provide --
// and nothing else.
func (docker *DockerLauncher) buildSidecarRequest(
	name string,
	imageID string,
	channel hostmcp.Channel,
	set hostmcp.Set,
	projectRoot string,
) dockercli.CreateRequest {
	return dockercli.CreateRequest{
		Image:          imageID,
		Name:           name,
		User:           strconv.Itoa(docker.HostUID) + ":" + strconv.Itoa(docker.HostGID),
		NetworkMode:    "host",
		ReadOnlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		// No Runtime: the sidecar is the only container this project creates with the Docker
		// default, because it runs no nested workload.
		Labels: []dockercli.KeyValue{
			// A role marker distinct from codex-safe.managed, which stays reserved for session
			// containers and their discovery filters.
			{Key: hostMCPSidecarLabel, Value: "true"},
			{Key: projectPathLabel, Value: projectRoot},
			{Key: hostUIDLabel, Value: strconv.Itoa(docker.HostUID)},
			{Key: hostMCPLabel, Value: set.Label()},
			{Key: hostMCPChannelLabel, Value: channel.Generation},
			{Key: hostMCPImageLabel, Value: imageID},
		},
		Mounts:  []dockercli.Mount{{Source: channel.Parent, Target: hostmcp.SidecarTarget}},
		Command: set.RelayCommand(channel, sidecarInitialLeaseTimeout),
	}
}

// ensureSidecar creates the relay sidecar, adopting one that is already running and awaiting the
// release of a name a departing sidecar still holds.
//
// A running container under this name is this session's own sidecar: the name embeds a random
// generation, so holding it requires already knowing it, and the only way to learn it is through the
// host Docker daemon, which means the invoking user. A stopped one is Docker's asynchronous --rm
// removal, not a competing owner, so it is awaited rather than treated as a conflict.
func (attempt *launchAttempt) ensureSidecar(
	ctx context.Context,
	name string,
	imageID string,
	channel hostmcp.Channel,
	set hostmcp.Set,
) error {
	request := attempt.docker.buildSidecarRequest(name, imageID, channel, set, attempt.plan.ProjectRoot)
	_, conflict, err := attempt.cli.Create(ctx, request)
	if err != nil {
		return fmt.Errorf("create host MCP relay sidecar: %w", err)
	}
	if !conflict {
		return nil
	}

	inspection, found, err := attempt.cli.Inspect(ctx, name)
	if err != nil {
		return fmt.Errorf("inspect host MCP relay sidecar %q: %w", name, err)
	}
	if found && inspection.State.Running {
		return nil
	}
	// Wait boundedly for Docker to release the name, then retry creation exactly once, rather than
	// proceeding into a readiness timeout that would report the wrong cause.
	if err := attempt.awaitSidecarNameRelease(ctx, name); err != nil {
		return err
	}
	if _, conflict, err = attempt.cli.Create(ctx, request); err != nil {
		return fmt.Errorf("recreate host MCP relay sidecar: %w", err)
	}
	if conflict {
		return fmt.Errorf(
			"host MCP relay sidecar name %q was still in use after waiting %s for Docker to release it",
			name, containerStateTimeout)
	}
	return nil
}

func (attempt *launchAttempt) awaitSidecarNameRelease(ctx context.Context, name string) error {
	waitContext, cancel := context.WithTimeout(ctx, containerStateTimeout)
	defer cancel()
	ticker := time.NewTicker(containerPollInterval)
	defer ticker.Stop()
	for {
		inspection, found, err := attempt.cli.Inspect(waitContext, name)
		if err != nil {
			return fmt.Errorf("inspect host MCP relay sidecar %q: %w", name, err)
		}
		if !found {
			return nil
		}
		if inspection.State.Running {
			// It came back to life between our create and this inspect: it is ours, and it serves.
			return nil
		}
		select {
		case <-waitContext.Done():
			return fmt.Errorf(
				"host MCP relay sidecar name %q was not released after %s (state %q)",
				name, containerStateTimeout, inspection.State.Status)
		case <-ticker.C:
		}
	}
}

// stopSidecar stops and awaits a candidate this attempt created and then lost the race with. It
// removes only this attempt's own sidecar and adopts nothing.
func (attempt *launchAttempt) stopSidecar(ctx context.Context, name string) error {
	if err := attempt.cli.Stop(ctx, name, containerStopTimeout); err != nil {
		return err
	}
	return attempt.awaitSidecarNameRelease(ctx, name)
}

// awaitChannelReady probes control.sock until the channel reports ready.
//
// It watches both containers while it waits. A session that dies during bootstrap -- because a
// listener could not bind, or its lease could not open -- would otherwise leave the launcher
// probing a socket nobody will ever publish and reporting a readiness timeout instead of the real
// diagnostic.
func (attempt *launchAttempt) awaitChannelReady(
	ctx context.Context,
	channel hostmcp.Channel,
	sidecar string,
	sessionID string,
) error {
	deadline := time.Now().Add(readinessTimeout)
	var lastProbe error
	for time.Now().Before(deadline) {
		ready, err := probeChannelReady(ctx, channel.ControlPath())
		if err == nil && ready {
			return nil
		}
		lastProbe = err
		if err := attempt.requireHostMCPContainersAlive(ctx, sidecar, sessionID); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readinessPollInterval):
		}
	}
	return fmt.Errorf(
		"host MCP channel %q did not report ready within %s (last probe: %v)",
		channel.ControlPath(), readinessTimeout, lastProbe)
}

// requireHostMCPContainersAlive fails the wait as soon as either container is gone, naming the one
// that died so the user sees the cause rather than the symptom.
func (attempt *launchAttempt) requireHostMCPContainersAlive(ctx context.Context, sidecar, sessionID string) error {
	for _, container := range []struct{ role, reference string }{
		{"session", sessionID},
		{"relay sidecar", sidecar},
	} {
		inspection, found, err := attempt.cli.Inspect(ctx, container.reference)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf(
				"the host MCP %s container %q exited while the launcher waited for the channel; "+
					"check `docker logs %s` for its diagnostic",
				container.role, container.reference, container.reference)
		}
		if !inspection.State.Running {
			return fmt.Errorf(
				"the host MCP %s container %q is %q while the launcher waited for the channel; "+
					"check `docker logs %s` for its diagnostic",
				container.role, container.reference, inspection.State.Status, container.reference)
		}
	}
	return nil
}

// probeChannelReady asks the channel whether every endpoint is bound and the lease exists.
//
// Readiness is never probed on a data socket: connecting to one would make the relay dial the real
// host MCP server and hand it a connection that immediately closes, which is a visible, confusing
// event on a server the launcher does not own.
func probeChannelReady(ctx context.Context, controlPath string) (bool, error) {
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", controlPath)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := connection.Write([]byte{mcpchannel.RoleProbe}); err != nil {
		return false, err
	}
	answer := make([]byte, 1)
	if _, err := io.ReadFull(connection, answer); err != nil {
		return false, err
	}
	return answer[0] == mcpchannel.Ready, nil
}

// hostMCPMismatchError reports a running session forwarding a different endpoint set. The launcher
// never terminates the other session and never proceeds with stale forwarders.
type hostMCPMismatchError struct {
	projectRoot string
	running     string
	requested   string
}

func (err *hostMCPMismatchError) Error() string {
	if err.requested == hostmcp.AbsentLabel {
		// The user asked for less access than the session already has. The mount is creation-time
		// state and docker exec cannot remove it, so naming this case beats reporting a differing set.
		return fmt.Sprintf(
			"a managed session for worktree %q is already forwarding host MCP endpoints (%s), and "+
				"--no-host-mcp cannot narrow a running session because the socket mount is fixed at "+
				"creation; finish the active session before retrying",
			err.projectRoot, err.running)
	}
	return fmt.Sprintf(
		"a managed session for worktree %q is already forwarding host MCP endpoints %q, but this "+
			"launch resolved %q; finish the active session before retrying, then relaunch",
		err.projectRoot, err.running, err.requested)
}

// validateRunningHostMCP compares the reuse label. It is the compared label; the channel label is
// read and never compared, because a reusing launcher legitimately computes a different candidate.
func (attempt *launchAttempt) validateRunningHostMCP(
	inspection dockercli.ContainerInspection,
	set hostmcp.Set,
) error {
	running := inspection.Config.Labels[hostMCPLabel]
	if running == "" {
		// A container created before this feature existed forwards nothing.
		running = hostmcp.AbsentLabel
	}
	if running != set.Label() {
		return &hostMCPMismatchError{
			projectRoot: attempt.plan.ProjectRoot,
			running:     running,
			requested:   set.Label(),
		}
	}
	return nil
}

// adoptedChannel reads a running session's channel from its label, so a reusing launcher works with
// the directory that session's sidecar actually serves.
func adoptedChannel(inspection dockercli.ContainerInspection) (hostmcp.Channel, error) {
	generation := inspection.Config.Labels[hostMCPChannelLabel]
	if generation == "" {
		return hostmcp.Channel{}, errors.New("the running session records no host MCP channel")
	}
	return hostmcp.AdoptChannel(generation)
}
