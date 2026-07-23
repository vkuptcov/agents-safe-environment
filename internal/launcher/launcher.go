package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/terminal"
)

// DockerLauncher resolves host inputs and runs commands in the worktree's managed Sysbox container.
// Container lifecycle policy stays in this package; Docker transport uses the host Docker CLI.
type DockerLauncher struct {
	// DockerBinary is the host Docker CLI executable.
	DockerBinary string
	// CommandRunner executes host Docker CLI commands.
	CommandRunner dockercli.Runner
	// HostOS is the host operating system checked by the launcher preflight.
	HostOS string
	// Stdin is forwarded to commands in the container.
	Stdin io.Reader
	// Stdout receives command output from the container.
	Stdout io.Writer
	// Stderr receives launcher and container diagnostics.
	Stderr io.Writer
	// HostUID is the invoking user's numeric UID forwarded to the container entrypoint.
	HostUID int
	// HostGID is the invoking user's numeric GID forwarded to the container entrypoint.
	HostGID int
	// HostUser is the invoking user's login name recreated inside the container.
	HostUser string
	// HostGroup is the invoking user's primary group name recreated inside the container.
	HostGroup string
	// HostHome is the absolute host home path recreated as a container-local directory.
	// The directory itself is not mounted from the host.
	HostHome string
	// AllocateTTY controls whether Docker allocates a terminal for the command.
	AllocateTTY bool
	// LookupEnv reads host environment variables during host-MCP channel allocation.
	LookupEnv func(string) (string, bool)
}

// launchAttempt carries the values that stay fixed for one Launch: the Docker client and the
// container the lifecycle steps operate on. The steps read them from here instead of threading them
// through every signature.
type launchAttempt struct {
	docker        *DockerLauncher
	cli           *dockercli.Client
	plan          launchplan.Plan
	image         string
	imageOverride bool
	containerName string
	projectKey    string
	// noHostMCP skips discovery entirely for this launch.
	noHostMCP bool
	// forceExec permits reuse when only the immutable creation fingerprint differs. Whether a given
	// running container is being force-adopted is derived from its inspection, so no per-inspection
	// state is retained across the polled fingerprint predicate.
	forceExec bool
	// launchFingerprint is the resolved immutable creation contract, computed before any container
	// adoption, image preparation, sidecar allocation, or create request.
	launchFingerprint string
	// hostMCP is this attempt's forwarding decision. Its zero value forwards nothing, which is the
	// zero-cost path through every lifecycle step.
	hostMCP hostMCPPlan
	// hostMCPImageID pins both containers to one immutable image, because they implement one private
	// protocol and a compatible tag is not enough.
	hostMCPImageID string
}

// NewDockerLauncher creates a launcher backed by the host Docker CLI, current process streams, and the canonical home
// already used to resolve project configuration.
func NewDockerLauncher(hostHome string) (*DockerLauncher, error) {
	if err := validateHostHome(hostHome); err != nil {
		return nil, err
	}
	hostUID := os.Getuid()
	hostGID := os.Getgid()
	hostUser, err := user.LookupId(strconv.Itoa(hostUID))
	if err != nil {
		return nil, fmt.Errorf("look up host user %d: %w", hostUID, err)
	}
	hostGroup, err := user.LookupGroupId(strconv.Itoa(hostGID))
	if err != nil {
		return nil, fmt.Errorf("look up host group %d: %w", hostGID, err)
	}
	docker := &DockerLauncher{
		DockerBinary:  "docker",
		CommandRunner: dockercli.NewProcessRunner(),
		HostOS:        runtime.GOOS,
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		HostUID:       hostUID,
		HostGID:       hostGID,
		HostUser:      hostUser.Username,
		HostGroup:     hostGroup.Name,
		HostHome:      hostHome,
		AllocateTTY:   terminal.IsTerminal(os.Stdin) && terminal.IsTerminal(os.Stdout),
		LookupEnv:     os.LookupEnv,
	}
	return docker, nil
}

// lookupEnv returns this launcher's environment reader, defaulting to the process environment.
func (docker *DockerLauncher) lookupEnv() func(string) (string, bool) {
	if docker.LookupEnv != nil {
		return docker.LookupEnv
	}
	return os.LookupEnv
}

// Launch runs one command in the deterministic project container, creating it when necessary.
func (docker *DockerLauncher) Launch(
	ctx context.Context,
	plan launchplan.Plan,
	image string,
	command []string,
	options launchplan.Options,
) error {
	if err := docker.validateConfiguration(); err != nil {
		return err
	}
	if len(command) == 0 {
		return errors.New("command is required")
	}
	if strings.TrimSpace(image) == "" {
		return errors.New("container image is required")
	}
	attempt := &launchAttempt{
		docker:        docker,
		cli:           dockercli.New(docker.DockerBinary, docker.CommandRunner),
		plan:          plan,
		image:         image,
		imageOverride: options.ImageOverride,
		containerName: ProjectContainerName(docker.HostUID, plan.ProjectRoot),
		projectKey:    ProjectKey(docker.HostUID, plan.ProjectRoot),
		noHostMCP:     options.NoHostMCP,
		forceExec:     options.ForceExec,
	}
	// Discovery runs during preflight, before a container is created or reused, and its channel must
	// exist before either container because it is a bind mount.
	if err := attempt.planHostMCP(); err != nil {
		return err
	}
	fingerprint, err := creationFingerprint(
		plan, image, options.ImageOverride, options.NoHostMCP, options.UseHostPythonVenv, attempt.hostMCP.set,
	)
	if err != nil {
		return err
	}
	attempt.launchFingerprint = fingerprint

	containerID, err := attempt.acquireContainer(ctx)
	if err != nil {
		// A candidate this attempt allocated and never handed off is this attempt's to unwind:
		// stop its sidecar promptly rather than leaving it to its initial-lease timeout, and remove
		// its generation directory.
		if cleanupErr := attempt.cleanupCandidate(ctx); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}

	execErr := attempt.execCommand(ctx, command, containerID)
	if execErr == nil {
		return nil
	}
	if !isRetryableExecError(execErr) {
		return execErr
	}
	retry, retryErr := attempt.containerStoppedAfterExec(ctx)
	if retryErr != nil {
		return errors.Join(execErr, retryErr)
	}
	if !retry {
		return execErr
	}

	// The first session shut down. Its old generation belongs to its own sidecar, which removes it on
	// lease EOF, so the replacement gets a fresh candidate rather than reusing a generation another
	// sidecar may be cleaning up.
	if err := attempt.reallocateHostMCPCandidate(); err != nil {
		return errors.Join(execErr, err)
	}
	containerID, err = attempt.acquireContainer(ctx)
	if err != nil {
		if cleanupErr := attempt.cleanupCandidate(ctx); cleanupErr != nil {
			return errors.Join(execErr, err, cleanupErr)
		}
		return errors.Join(execErr, err)
	}
	if err := attempt.execCommand(ctx, command, containerID); err != nil {
		return fmt.Errorf("exec in replacement Sysbox container: %w", err)
	}
	return nil
}
