package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	sysboxRuntime    = "sysbox-runc"
	sessionLabel     = "codex-safe.session"
	projectPathLabel = "codex-safe.project-path"
	hostUIDLabel     = "codex-safe.host-uid"
)

// CommandRunner makes Docker process execution replaceable in focused tests.
type CommandRunner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
	Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error
}

// Docker launches an outer container through the host Docker CLI.
type Docker struct {
	// Binary is the host Docker CLI executable.
	Binary string
	// GOOS is the host operating system checked by the launcher preflight.
	GOOS string
	// Runner executes host Docker CLI commands.
	Runner CommandRunner
	// Stdin is forwarded to the outer container.
	Stdin io.Reader
	// Stdout receives output from the outer container.
	Stdout io.Writer
	// Stderr receives launcher and outer-container diagnostics.
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
	// TTY controls whether Docker allocates a terminal for the outer container.
	TTY bool
	// NameGenerator creates a unique Docker container name for each session.
	NameGenerator func() (string, error)
}

// NewDocker creates a launcher backed by os/exec and the current process streams.
func NewDocker() (*Docker, error) {
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
	hostHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve host home directory: %w", err)
	}
	hostHome = filepath.Clean(hostHome)
	if hostHome == "/" {
		return nil, errors.New("host home directory cannot be the filesystem root")
	}
	if err := validateMountPath("host home directory", hostHome); err != nil {
		return nil, err
	}
	hostGitConfig, err := discoverHostGitConfig(hostHome)
	if err != nil {
		return nil, err
	}

	return &Docker{
		Binary:        "docker",
		GOOS:          runtime.GOOS,
		Runner:        execCommandRunner{},
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		HostUID:       hostUID,
		HostGID:       hostGID,
		HostUser:      hostUser.Username,
		HostGroup:     hostGroup.Name,
		HostHome:      hostHome,
		HostGitConfig: hostGitConfig,
		TTY:           isTerminal(os.Stdin) && isTerminal(os.Stdout),
		NameGenerator: randomSessionName,
	}, nil
}

// Launch executes the probe in an active project container or starts a new ephemeral one.
func (docker *Docker) Launch(ctx context.Context, plan Plan, image string, probe []string) error {
	if err := docker.validateConfiguration(); err != nil {
		return err
	}
	if len(probe) == 0 {
		return errors.New("probe command is required")
	}
	if strings.TrimSpace(image) == "" {
		return errors.New("container image is required")
	}
	if _, err := validatePlan(plan); err != nil {
		return err
	}

	containerID, err := docker.findRunningProjectContainer(ctx, plan.ProjectRoot)
	if err != nil {
		return err
	}
	if containerID != "" {
		if err := docker.waitForProjectContainer(ctx, containerID); err != nil {
			return err
		}
		args, err := BuildDockerExecArgs(
			plan,
			probe,
			containerID,
			docker.HostUID,
			docker.HostGID,
			docker.HostHome,
			docker.TTY,
		)
		if err != nil {
			return err
		}
		if err := docker.Runner.Run(
			ctx,
			docker.Binary,
			args,
			docker.Stdin,
			docker.Stdout,
			docker.Stderr,
		); err != nil {
			return fmt.Errorf("exec in active Sysbox container: %w", err)
		}
		return nil
	}

	if err := docker.preflight(ctx, image); err != nil {
		return err
	}

	sessionName, err := docker.NameGenerator()
	if err != nil {
		return fmt.Errorf("generate session name: %w", err)
	}
	if err := validateSessionName(sessionName); err != nil {
		return err
	}

	args, err := BuildDockerArgs(
		plan,
		image,
		probe,
		sessionName,
		docker.HostUID,
		docker.HostGID,
		docker.HostUser,
		docker.HostGroup,
		docker.HostHome,
		docker.HostGitConfig,
		docker.TTY,
	)
	if err != nil {
		return err
	}
	if err := docker.Runner.Run(
		ctx,
		docker.Binary,
		args,
		docker.Stdin,
		docker.Stdout,
		docker.Stderr,
	); err != nil {
		return fmt.Errorf("run Sysbox container: %w", err)
	}
	return nil
}

func (docker *Docker) waitForProjectContainer(ctx context.Context, containerID string) error {
	const waitCommand = `for ((attempt = 0; attempt < 240; attempt++)); do
	[[ -e /run/codex-safe/ready ]] && exit 0
	sleep 0.25
done
exit 1`

	output, err := docker.Runner.CombinedOutput(
		ctx,
		docker.Binary,
		"exec",
		containerID,
		"bash",
		"-c",
		waitCommand,
	)
	if err != nil {
		return commandFailure("wait for active codex-safe project container", output, err)
	}
	return nil
}

func (docker *Docker) findRunningProjectContainer(ctx context.Context, projectRoot string) (string, error) {
	output, err := docker.Runner.CombinedOutput(
		ctx,
		docker.Binary,
		"container",
		"ls",
		"--quiet",
		"--no-trunc",
		"--filter",
		"label="+sessionLabel,
		"--filter",
		"label="+projectPathLabel+"="+projectRoot,
		"--filter",
		"label="+hostUIDLabel+"="+strconv.Itoa(docker.HostUID),
	)
	if err != nil {
		return "", commandFailure("find active codex-safe project container", output, err)
	}

	containerIDs := strings.Fields(string(output))
	for _, containerID := range containerIDs {
		if err := validateContainerID(containerID); err != nil {
			return "", fmt.Errorf("parse active codex-safe project container: %w", err)
		}
	}
	if len(containerIDs) > 1 {
		return "", fmt.Errorf(
			"multiple active codex-safe containers manage project %q: %s",
			projectRoot,
			strings.Join(containerIDs, ", "),
		)
	}
	if len(containerIDs) == 1 {
		return containerIDs[0], nil
	}
	return "", nil
}

func (docker *Docker) validateConfiguration() error {
	if docker == nil {
		return errors.New("Docker launcher is nil")
	}
	if docker.Binary == "" {
		return errors.New("Docker binary is empty")
	}
	if docker.GOOS != "linux" {
		return fmt.Errorf("unsupported host OS %q: the MVP requires Linux", docker.GOOS)
	}
	if docker.Runner == nil {
		return errors.New("Docker command runner is nil")
	}
	if docker.NameGenerator == nil {
		return errors.New("session name generator is nil")
	}
	if docker.HostUID < 0 || docker.HostGID < 0 {
		return fmt.Errorf("invalid host identity %d:%d", docker.HostUID, docker.HostGID)
	}
	if err := validateAccountName("host user", docker.HostUser); err != nil {
		return err
	}
	if err := validateAccountName("host group", docker.HostGroup); err != nil {
		return err
	}
	if docker.HostHome == "/" {
		return errors.New("host home directory cannot be the filesystem root")
	}
	if err := validateMountPath("host home directory", docker.HostHome); err != nil {
		return err
	}
	if docker.HostGitConfig != "" {
		if err := validateMountPath("host Git config", docker.HostGitConfig); err != nil {
			return err
		}
	}
	return nil
}

func (docker *Docker) preflight(ctx context.Context, image string) error {
	if strings.TrimSpace(image) == "" {
		return errors.New("container image is required")
	}

	output, err := docker.Runner.CombinedOutput(
		ctx,
		docker.Binary,
		"info",
		"--format",
		"{{json .Runtimes}}",
	)
	if err != nil {
		return commandFailure("query Docker runtimes", output, err)
	}

	runtimes := make(map[string]json.RawMessage)
	if err := json.Unmarshal(output, &runtimes); err != nil {
		return fmt.Errorf("parse Docker runtimes: %w", err)
	}
	if _, found := runtimes[sysboxRuntime]; !found {
		return fmt.Errorf("Docker runtime %q is not registered", sysboxRuntime)
	}

	output, err = docker.Runner.CombinedOutput(ctx, docker.Binary, "image", "inspect", image)
	if err != nil {
		return commandFailure(fmt.Sprintf("inspect image %q", image), output, err)
	}
	return nil
}

// BuildDockerArgs returns argv for one outer-container launch without invoking a shell.
func BuildDockerArgs(
	plan Plan,
	image string,
	probe []string,
	sessionName string,
	hostUID int,
	hostGID int,
	hostUser string,
	hostGroup string,
	hostHome string,
	hostGitConfig string,
	tty bool,
) ([]string, error) {
	if strings.TrimSpace(image) == "" {
		return nil, errors.New("container image is required")
	}
	if len(probe) == 0 {
		return nil, errors.New("probe command is required")
	}
	if err := validateSessionName(sessionName); err != nil {
		return nil, err
	}
	if hostUID < 0 || hostGID < 0 {
		return nil, fmt.Errorf("invalid host identity %d:%d", hostUID, hostGID)
	}
	if err := validateAccountName("host user", hostUser); err != nil {
		return nil, err
	}
	if err := validateAccountName("host group", hostGroup); err != nil {
		return nil, err
	}
	if hostHome == "/" {
		return nil, errors.New("host home directory cannot be the filesystem root")
	}
	if err := validateMountPath("host home directory", hostHome); err != nil {
		return nil, err
	}
	if hostGitConfig != "" {
		if err := validateMountPath("host Git config", hostGitConfig); err != nil {
			return nil, err
		}
	}
	mounts, err := validatePlan(plan)
	if err != nil {
		return nil, err
	}

	args := []string{
		"run",
		"--rm",
		"--interactive",
	}
	if tty {
		args = append(args, "--tty")
	}
	args = append(args,
		"--runtime="+sysboxRuntime,
		"--name",
		sessionName,
		"--label",
		sessionLabel+"="+sessionName,
		"--label",
		projectPathLabel+"="+plan.ProjectRoot,
		"--label",
		hostUIDLabel+"="+strconv.Itoa(hostUID),
		"--env",
		"CODEX_SAFE_HOST_UID="+strconv.Itoa(hostUID),
		"--env",
		"CODEX_SAFE_HOST_GID="+strconv.Itoa(hostGID),
		"--env",
		"CODEX_SAFE_HOST_USER="+hostUser,
		"--env",
		"CODEX_SAFE_HOST_GROUP="+hostGroup,
		"--env",
		"CODEX_SAFE_HOST_HOME="+hostHome,
		"--workdir",
		plan.WorkingDir,
	)
	if hostGitConfig != "" {
		containerGitConfig := filepath.Join(hostHome, ".gitconfig")
		specification := "type=bind,source=" + hostGitConfig + ",target=" + containerGitConfig
		specification += ",bind-propagation=rprivate,readonly"
		args = append(args, "--mount", specification)
	}
	for _, mount := range mounts {
		specification := "type=bind,source=" + mount.Source + ",target=" + mount.Target
		specification += ",bind-propagation=rprivate"
		if mount.ReadOnly {
			specification += ",readonly"
		}
		args = append(args, "--mount", specification)
	}
	args = append(args, image)
	args = append(args, probe...)

	return args, nil
}

// BuildDockerExecArgs returns argv for a probe in an already-running project container.
func BuildDockerExecArgs(
	plan Plan,
	probe []string,
	containerID string,
	hostUID int,
	hostGID int,
	hostHome string,
	tty bool,
) ([]string, error) {
	if len(probe) == 0 {
		return nil, errors.New("probe command is required")
	}
	if err := validateContainerID(containerID); err != nil {
		return nil, err
	}
	if hostUID < 0 || hostGID < 0 {
		return nil, fmt.Errorf("invalid host identity %d:%d", hostUID, hostGID)
	}
	if hostHome == "/" {
		return nil, errors.New("host home directory cannot be the filesystem root")
	}
	if err := validateMountPath("host home directory", hostHome); err != nil {
		return nil, err
	}
	if _, err := validatePlan(plan); err != nil {
		return nil, err
	}

	args := []string{
		"exec",
		"--interactive",
	}
	if tty {
		args = append(args, "--tty")
	}
	args = append(args,
		"--user",
		strconv.Itoa(hostUID)+":"+strconv.Itoa(hostGID),
		"--env",
		"HOME="+hostHome,
		"--workdir",
		plan.WorkingDir,
		containerID,
	)
	args = append(args, probe...)

	return args, nil
}

func validateContainerID(containerID string) error {
	if len(containerID) < 12 || len(containerID) > 64 || len(containerID)%2 != 0 {
		return fmt.Errorf("invalid Docker container ID %q", containerID)
	}
	if _, err := hex.DecodeString(containerID); err != nil {
		return fmt.Errorf("invalid Docker container ID %q", containerID)
	}
	return nil
}

func discoverHostGitConfig(hostHome string) (string, error) {
	path, err := filepath.Abs(filepath.Join(hostHome, ".gitconfig"))
	if err != nil {
		return "", fmt.Errorf("resolve host Git config path: %w", err)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect host Git config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("host Git config %q is not a regular file", path)
	}

	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("canonicalize host Git config %q: %w", path, err)
	}
	return filepath.Clean(canonical), nil
}

func validateAccountName(label string, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is empty", label)
	}
	for index, character := range name {
		first := index == 0
		last := index == len(name)-1
		allowed := character >= 'a' && character <= 'z' ||
			!first && character >= '0' && character <= '9' ||
			character == '_' ||
			!first && character == '-' ||
			last && character == '$'
		if !allowed {
			return fmt.Errorf("%s name %q is unsupported", label, name)
		}
	}
	return nil
}

// isTerminal uses the Linux terminal ioctl so other character devices, such as /dev/null, are not treated as TTYs.
func isTerminal(file *os.File) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		file.Fd(),
		syscall.TCGETS,
		uintptr(unsafe.Pointer(&termios)),
		0,
		0,
		0,
	)
	return errno == 0
}

func validateSessionName(name string) error {
	if name == "" {
		return errors.New("session name is empty")
	}
	for index, character := range name {
		allowed := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '_' || character == '.' || character == '-')
		if !allowed {
			return fmt.Errorf("session name %q contains unsupported characters", name)
		}
	}
	return nil
}

func randomSessionName() (string, error) {
	identifier := make([]byte, 6)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}
	return "codex-safe-" + hex.EncodeToString(identifier), nil
}

func commandFailure(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, message)
}

type execCommandRunner struct{}

func (execCommandRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (execCommandRunner) Run(
	ctx context.Context,
	name string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}
