package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

const (
	hostMCPLabel        = "agents-safe.host-mcp"
	hostMCPChannelLabel = "agents-safe.host-mcp-channel"
	hostMCPSidecarLabel = "agents-safe.host-mcp-sidecar"
	hostMCPImageLabel   = "agents-safe.host-mcp-image"
	hostMCPEnv          = "AGENTS_SAFE_HOST_MCP"

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
	// sessionID is set once this attempt's session container is created. A post-create failure must
	// stop that session before the generation is removed, or the session would keep the directory
	// bind-mounted while its host pathname disappears, breaking a later sidecar's recovery.
	sessionID string
}

// removeCandidate discards a generation this attempt allocated and then did not use.
func (forwarding hostMCPPlan) removeCandidate() error {
	if !forwarding.candidate {
		return nil
	}
	return forwarding.channel.Remove()
}

// cleanupCandidate unwinds a candidate this attempt allocated but did not hand off: it stops the
// session container it created, then the candidate sidecar it started, then removes the generation
// directory.
//
// It removes only this attempt's own resources and adopts nothing, so a launch that already adopted
// a running session's channel is left untouched. Three rules keep it safe:
//
//   - The session is stopped before the generation is removed, so a still-running session never has
//     its bind-mounted channel unlinked from under it.
//   - The generation is removed only if every stop succeeded, so a container that could not be
//     stopped keeps its directory.
//   - Cleanup runs on a context detached from the launch's, so a cancelled or timed-out launch still
//     unwinds rather than skipping the stops and unlinking a live channel anyway.
func (attempt *launchAttempt) cleanupCandidate(parent context.Context) error {
	if !attempt.hostMCP.candidate {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), containerStateTimeout)
	defer cancel()

	var errs []error
	if attempt.hostMCP.sessionID != "" {
		if err := attempt.cli.Stop(ctx, attempt.containerName, containerStopTimeout); err != nil {
			errs = append(errs, fmt.Errorf("stop session after failed host MCP launch: %w", err))
		}
	}
	if attempt.hostMCP.sidecarStarted {
		name := sidecarName(attempt.projectKey, attempt.hostMCP.channel)
		if err := attempt.stopSidecar(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		if err := attempt.hostMCP.removeCandidate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sidecarName is the relay sidecar's deterministic name.
//
// The generation makes each launch attempt's name distinct, so two attempts both create a sidecar
// and the session container's name remains the sole arbiter of creation. It also keeps successive
// sessions distinct, so a departing sidecar cannot hold the name an arriving one needs.
func sidecarName(projectKey string, channel hostmcp.Channel) string {
	return "agents-safe-mcp-" + projectKey + "-" + channel.Name
}

// planHostMCP resolves the forwarded endpoint set before the creation fingerprint is computed.
//
// Discovery reads the Codex and Claude host state this launch already resolved. --no-host-mcp or an
// omitted channel role skips both sources. An empty endpoint union allocates nothing: no directory,
// environment variable, mount, relay, or banner line.
func (attempt *launchAttempt) planHostMCP() error {
	if attempt.noHostMCP || !attempt.plan.HostMCPChannel {
		// Disabled forwarding and an omitted logical channel role perform no product config read.
		return nil
	}
	sources := hostmcp.Sources{ProjectRoot: attempt.plan.ProjectRoot}
	if mount, found := attempt.plan.MountForRole(projectenv.RoleCodexHome); found {
		sources.CodexHome = mount.Source
	}
	if mount, found := attempt.plan.MountForRole(projectenv.RoleClaudeConfig); found {
		sources.ClaudeConfigFile = mount.Source
	} else if mount, found := attempt.plan.MountForRole(projectenv.RoleClaudeHome); found {
		sources.ClaudeConfigFile = filepath.Join(mount.Source, ".claude.json")
	}
	set, err := hostmcp.DiscoverAll(sources)
	if err != nil {
		return err
	}
	if set.Empty() {
		return nil
	}
	attempt.hostMCP.set = set
	return nil
}

// allocateHostMCPCandidate performs the first host-side mutation for a cold forwarding session.
// It must run only after the fingerprint has ruled out running-container adoption.
func (attempt *launchAttempt) allocateHostMCPCandidate() error {
	if attempt.hostMCP.set.Empty() || attempt.hostMCP.candidate {
		return nil
	}
	channel, err := hostmcp.NewChannel(
		attempt.docker.lookupEnv(), attempt.projectKey, len(attempt.hostMCP.set.Endpoints),
	)
	if err != nil {
		return err
	}
	attempt.hostMCP.channel = channel
	attempt.hostMCP.candidate = true
	return nil
}

// reallocateHostMCPCandidate allocates a fresh generation for a replacement session. A previous
// generation may still belong to a departing sidecar, so it is never removed or reused here.
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
	attempt.hostMCP.channel = channel
	attempt.hostMCP.candidate = true
	attempt.hostMCP.sidecarStarted = false
	attempt.hostMCP.sessionID = ""
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
) (string, bool, error) {
	if err := attempt.allocateHostMCPCandidate(); err != nil {
		return "", false, err
	}
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
	// When this launch forwards MCP, the session create is the launcher's half of the cold-start
	// bound: a wedged `docker run` that outlasts it would let the sidecar reach its initial-lease
	// timeout before serve could ever lease. Bound it so that bound is a fact, not an assumption. A
	// launch that forwards nothing keeps the parent context and its existing behavior.
	createCtx := ctx
	if !attempt.hostMCP.set.Empty() {
		bounded, cancel := context.WithTimeout(ctx, sessionCreateTimeout)
		defer cancel()
		createCtx = bounded
	}
	containerID, conflict, err := attempt.createContainer(createCtx)
	if err != nil || conflict {
		return containerID, conflict, err
	}
	// The session now exists, so a later failure must stop it before removing the generation.
	attempt.hostMCP.sessionID = containerID
	if err := attempt.awaitSessionReady(ctx, containerID); err != nil {
		return "", false, err
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
// winner's resources are not this attempt's to touch. Once its generation is removed, this attempt
// no longer owns a candidate; a later cold retry allocates a fresh one.
func (attempt *launchAttempt) discardHostMCPCandidate(ctx context.Context) error {
	if err := attempt.cleanupCandidate(ctx); err != nil {
		return err
	}
	attempt.hostMCP.candidate = false
	attempt.hostMCP.sidecarStarted = false
	return nil
}

// reuseHostMCPAfterWait re-inspects a container that became reusable while this attempt waited, and
// applies post-wait adoption to it. A launch that forwards nothing remains a no-op unless --force-exec
// must report that it is bypassing a fingerprint mismatch.
func (attempt *launchAttempt) reuseHostMCPAfterWait(ctx context.Context) error {
	if attempt.hostMCP.set.Empty() && !attempt.forceExec {
		return nil
	}
	inspection, found, err := attempt.inspectOwnedContainer(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("managed container %q vanished before host MCP reuse", attempt.containerName)
	}
	if err := attempt.validateRunningFingerprint(inspection); err != nil {
		return err
	}
	return attempt.reuseHostMCP(ctx, inspection)
}

// reuseHostMCP validates a running session's forwarding against this launch's resolution, adopts its
// channel, and recreates a sidecar that has died.
func (attempt *launchAttempt) reuseHostMCP(ctx context.Context, inspection dockercli.ContainerInspection) error {
	if attempt.forcedFingerprintMismatch(inspection) {
		// --force-exec only bypasses fingerprint equality. The current launch may have resolved a
		// different endpoint set, channel shape, or no forwarding at all, so host-MCP reconciliation
		// must not modify the already-running container's creation-time contract.
		running := inspection.Config.Labels[launchConfigLabel]
		fmt.Fprintf(
			attempt.docker.Stderr,
			"warning: --force-exec: executing in managed session container %q (ID %q) despite "+
				"creation fingerprint mismatch (running %q, requested %q); "+
				"the container's existing creation-time configuration remains in effect\n",
			attempt.containerName, inspection.ID, running, attempt.launchFingerprint,
		)
		if err := attempt.hostMCP.removeCandidate(); err != nil {
			return err
		}
		attempt.hostMCP.candidate = false
		return nil
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
			// A role marker distinct from agents-safe.managed, which stays reserved for session
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
	// Wait boundedly for Docker to release the name. If the sidecar becomes running while we wait it
	// is ours and is adopted; only a released name is retried, exactly once, rather than proceeding
	// into a readiness timeout that would report the wrong cause.
	outcome, err := attempt.awaitSidecarName(ctx, name, true)
	if err != nil {
		return err
	}
	if outcome == sidecarBecameRunning {
		return nil
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

// sidecarNameOutcome distinguishes the two ways a bounded name wait can succeed. Conflating them
// makes a launcher retry a create that then falsely conflicts with a sidecar that just became this
// session's own.
type sidecarNameOutcome int

const (
	sidecarNameReleased sidecarNameOutcome = iota
	sidecarBecameRunning
)

// awaitSidecarName waits, boundedly, for a sidecar name to be released. adoptOnRunning selects what
// a running observation means: during recovery it means the sidecar is this session's own and is
// adopted; after a Stop it is a transient state on the way to removal and the wait continues.
func (attempt *launchAttempt) awaitSidecarName(
	ctx context.Context,
	name string,
	adoptOnRunning bool,
) (sidecarNameOutcome, error) {
	waitContext, cancel := context.WithTimeout(ctx, containerStateTimeout)
	defer cancel()
	ticker := time.NewTicker(containerPollInterval)
	defer ticker.Stop()
	for {
		inspection, found, err := attempt.cli.Inspect(waitContext, name)
		if err != nil {
			return sidecarNameReleased, fmt.Errorf("inspect host MCP relay sidecar %q: %w", name, err)
		}
		if !found {
			return sidecarNameReleased, nil
		}
		if adoptOnRunning && inspection.State.Running {
			// It came back to life between our create and this inspect: it is ours, and it serves.
			return sidecarBecameRunning, nil
		}
		select {
		case <-waitContext.Done():
			return sidecarNameReleased, fmt.Errorf(
				"host MCP relay sidecar name %q was not released after %s (state %q)",
				name, containerStateTimeout, inspection.State.Status)
		case <-ticker.C:
		}
	}
}

// stopSidecar stops and awaits a candidate this attempt created and then lost the race with, or is
// unwinding after a failed launch. It removes only this attempt's own sidecar and adopts nothing.
//
// After a Stop, the container is on its way out, so a transient running observation is ignored: this
// waits for the name to be released. A stop failure on an already-removed container is success,
// which Stop already reports, so a not-found here is the expected end.
func (attempt *launchAttempt) stopSidecar(ctx context.Context, name string) error {
	if err := attempt.cli.Stop(ctx, name, containerStopTimeout); err != nil {
		return err
	}
	// adoptOnRunning is false: after a Stop the container is on its way out, so wait for the name to
	// be released rather than mistaking a transient running observation for adoption.
	if _, err := attempt.awaitSidecarName(ctx, name, false); err != nil {
		return err
	}
	return nil
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
	// One deadline governs the whole wait, including the probe dials and the liveness inspections, so
	// a hung Docker inspection or a wedged probe cannot exceed the advertised readiness bound.
	readyCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	var lastProbe error
	for {
		ready, err := probeChannelReady(readyCtx, channel.ControlPath())
		if err == nil && ready {
			return nil
		}
		lastProbe = err
		if err := attempt.requireHostMCPContainersAlive(readyCtx, sidecar, sessionID); err != nil {
			return err
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf(
				"host MCP channel %q did not report ready within %s (last probe: %v)",
				channel.ControlPath(), readinessTimeout, lastProbe)
		case <-time.After(readinessPollInterval):
		}
	}
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

// adoptedChannel reads a running session's channel from its label, so a reusing launcher works with
// the directory that session's sidecar actually serves.
func adoptedChannel(inspection dockercli.ContainerInspection) (hostmcp.Channel, error) {
	generation := inspection.Config.Labels[hostMCPChannelLabel]
	if generation == "" {
		return hostmcp.Channel{}, errors.New("the running session records no host MCP channel")
	}
	return hostmcp.AdoptChannel(generation)
}
