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
	// HostGitConfig is the canonical host path mounted read-only as the container's global Git config.
	// It is empty when the invoking environment has no $HOME/.gitconfig file.
	HostGitConfig string
	// HostEnvironment is the Docker-free host snapshot shared with project-default generation.
	HostEnvironment HostEnvironment
	// AllocateTTY controls whether Docker allocates a terminal for the command.
	AllocateTTY bool
	// CanPrompt reports whether stdin and the diagnostic stream can service an interactive host
	// prompt. It is intentionally independent from AllocateTTY, which also requires terminal stdout.
	CanPrompt bool
	// LookupEnv reads host environment variables during user-mount resolution.
	LookupEnv func(string) (string, bool)
	// CodexHomePolicy controls how a missing Codex home is handled.
	CodexHomePolicy CodexHomePolicy

	// resolveUserMounts overrides host user-mount resolution in tests. Production launchers leave it
	// nil and resolve from the filesystem; see resolveMounts.
	resolveUserMounts func(launchplan.Plan) (userMountResolution, error)
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
	// hostMCP is this attempt's forwarding decision. Its zero value forwards nothing, which is the
	// zero-cost path through every lifecycle step.
	hostMCP hostMCPPlan
	// hostMCPImageID pins both containers to one immutable image, because they implement one private
	// protocol and a compatible tag is not enough.
	hostMCPImageID string
}

// NewDockerLauncher creates a launcher backed by the host Docker CLI and current process streams.
func NewDockerLauncher(codexHomePolicy CodexHomePolicy) (*DockerLauncher, error) {
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
	// Construction stays limited to the historical identity/Git preflight. Optional Codex and skills discovery belongs
	// to the typed resolver, which Phase 2 invokes only after CLI usage validation and Git discovery.
	hostEnvironment, err := resolveHostIdentity(defaultHostEnvironmentInputs())
	if err != nil {
		return nil, err
	}

	docker := &DockerLauncher{
		DockerBinary:    "docker",
		CommandRunner:   dockercli.NewProcessRunner(),
		HostOS:          runtime.GOOS,
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		HostUID:         hostUID,
		HostGID:         hostGID,
		HostUser:        hostUser.Username,
		HostGroup:       hostGroup.Name,
		HostHome:        hostEnvironment.HomeDir,
		HostGitConfig:   hostEnvironment.GitConfig,
		HostEnvironment: hostEnvironment,
		AllocateTTY:     terminal.IsTerminal(os.Stdin) && terminal.IsTerminal(os.Stdout),
		CanPrompt:       terminal.IsTerminal(os.Stdin) && terminal.IsTerminal(os.Stderr),
		LookupEnv:       os.LookupEnv,
		CodexHomePolicy: codexHomePolicy,
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

// resolveMounts resolves this launch's user mounts through the test seam when one is injected, and
// from the host filesystem otherwise.
func (docker *DockerLauncher) resolveMounts(plan launchplan.Plan) (userMountResolution, error) {
	if docker.resolveUserMounts != nil {
		return docker.resolveUserMounts(plan)
	}
	return docker.defaultResolveUserMounts(plan)
}

func (docker *DockerLauncher) defaultResolveUserMounts(plan launchplan.Plan) (userMountResolution, error) {
	lookup := docker.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return inspectUserMounts(UserMountInputs{
		LookupEnv:       lookup,
		HomeDir:         docker.HostHome,
		WritableSources: writableMountSources(plan.Mounts),
		CodexHomePolicy: docker.CodexHomePolicy,
	})
}

// confirmCreateCodexHome offers to create a missing default Codex home after Docker preflight.
func (docker *DockerLauncher) confirmCreateCodexHome(path string) (bool, error) {
	if !docker.CanPrompt {
		return false, nil
	}
	fmt.Fprintf(docker.Stderr, "Codex home %q does not exist. Create it now? [Y/n] ", path)
	line, err := readPromptLine(docker.Stdin)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Codex-home confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		if err := os.MkdirAll(path, 0o700); err != nil {
			return false, fmt.Errorf("create Codex home %q: %w", path, err)
		}
		fmt.Fprintf(docker.Stderr, "Created Codex home %q\n", path)
		return true, nil
	default:
		return false, nil
	}
}

func readPromptLine(reader io.Reader) (string, error) {
	var line strings.Builder
	var buffer [1]byte
	for {
		count, err := reader.Read(buffer[:])
		if count == 1 {
			if buffer[0] == '\n' {
				return line.String(), nil
			}
			line.WriteByte(buffer[0])
		}
		if err != nil {
			return line.String(), err
		}
		if count == 0 {
			return line.String(), io.ErrNoProgress
		}
	}
}

func (docker *DockerLauncher) materializeUserMounts(
	plan launchplan.Plan,
	resolution userMountResolution,
) (UserMounts, error) {
	if resolution.missingCodexHome == "" {
		return resolution.mounts, nil
	}
	created, err := docker.confirmCreateCodexHome(resolution.missingCodexHome)
	if err != nil {
		return UserMounts{}, err
	}
	if !created {
		return UserMounts{}, fmt.Errorf("Codex home %q does not exist", resolution.missingCodexHome)
	}
	resolved, err := docker.resolveMounts(plan)
	if err != nil {
		return UserMounts{}, err
	}
	if resolved.missingCodexHome != "" {
		return UserMounts{}, fmt.Errorf("Codex home %q does not exist", resolved.missingCodexHome)
	}
	return resolved.mounts, nil
}

func writableMountSources(mounts []launchplan.BindMount) []string {
	sources := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		if !mount.ReadOnly {
			sources = append(sources, mount.Source)
		}
	}
	return sources
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
	}
	resolution, err := docker.resolveMounts(plan)
	if err != nil {
		return err
	}
	// Discovery runs during preflight, before a container is created or reused, and its channel must
	// exist before either container because it is a bind mount.
	if err := attempt.planHostMCP(resolution); err != nil {
		return err
	}

	containerID, userMounts, err := attempt.acquireContainer(ctx, resolution)
	if err != nil {
		// A candidate this attempt allocated and never handed off is this attempt's to unwind:
		// stop its sidecar promptly rather than leaving it to its initial-lease timeout, and remove
		// its generation directory.
		if cleanupErr := attempt.cleanupCandidate(ctx); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}

	execErr := attempt.execCommand(ctx, command, containerID, userMounts)
	if execErr == nil {
		return nil
	}
	if !isRetryableExecError(execErr) {
		return execErr
	}
	retry, retryErr := attempt.containerStoppedAfterExec(ctx, userMounts)
	if retryErr != nil {
		return errors.Join(execErr, retryErr)
	}
	if !retry {
		return execErr
	}

	// The first session shut down. Its old generation belongs to its own sidecar, which removes it on
	// lease EOF, so the replacement gets a fresh candidate rather than reusing a generation another
	// sidecar may be cleaning up. The mounts are already materialized, so they reuse them as resolved.
	if err := attempt.reallocateHostMCPCandidate(); err != nil {
		return errors.Join(execErr, err)
	}
	containerID, userMounts, err = attempt.acquireContainer(ctx, userMountResolution{mounts: userMounts})
	if err != nil {
		if cleanupErr := attempt.cleanupCandidate(ctx); cleanupErr != nil {
			return errors.Join(execErr, err, cleanupErr)
		}
		return errors.Join(execErr, err)
	}
	if err := attempt.execCommand(ctx, command, containerID, userMounts); err != nil {
		return fmt.Errorf("exec in replacement Sysbox container: %w", err)
	}
	return nil
}
