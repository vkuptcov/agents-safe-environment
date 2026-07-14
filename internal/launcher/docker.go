package launcher

import (
	"context"
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
	"time"
	"unsafe"

	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

const (
	sysboxRuntime        = "sysbox-runc"
	managedLabel         = "codex-safe.managed"
	projectPathLabel     = "codex-safe.project-path"
	hostUIDLabel         = "codex-safe.host-uid"
	managerProtocolLabel = "codex-safe.manager-protocol"
	managedLabelValue    = "true"

	containerStateTimeout = 20 * time.Second
	containerPollInterval = 50 * time.Millisecond
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
	}, nil
}

// Launch executes the command through the wrapper in the one deterministic
// project container, creating that detached container when necessary.
func (docker *Docker) Launch(ctx context.Context, plan Plan, image string, command []string) error {
	if err := docker.validateConfiguration(); err != nil {
		return err
	}
	if len(command) == 0 {
		return errors.New("command is required")
	}
	if strings.TrimSpace(image) == "" {
		return errors.New("container image is required")
	}
	if _, err := validatePlan(plan); err != nil {
		return err
	}
	containerName, err := ProjectContainerName(docker.HostUID, plan.ProjectRoot)
	if err != nil {
		return err
	}
	containerID, err := docker.acquireProjectContainer(ctx, plan, image, containerName)
	if err != nil {
		return err
	}

	execErr := docker.execProjectCommand(ctx, plan, command, containerID)
	if execErr == nil {
		return nil
	}
	if !isRetryableExecError(execErr) {
		return execErr
	}
	retry, retryErr := docker.containerStoppedAfterExec(ctx, plan, containerName)
	if retryErr != nil {
		return errors.Join(execErr, retryErr)
	}
	if !retry {
		return execErr
	}

	containerID, err = docker.acquireProjectContainer(ctx, plan, image, containerName)
	if err != nil {
		return errors.Join(execErr, err)
	}
	if err := docker.execProjectCommand(ctx, plan, command, containerID); err != nil {
		return fmt.Errorf("exec in replacement Sysbox container: %w", err)
	}
	return nil
}

func (docker *Docker) acquireProjectContainer(
	ctx context.Context,
	plan Plan,
	image string,
	containerName string,
) (string, error) {
	inspection, found, err := docker.inspectProjectContainer(ctx, containerName)
	if err != nil {
		return "", err
	}
	if found {
		if err := docker.validateProjectContainer(inspection, plan.ProjectRoot); err != nil {
			return "", err
		}
		if inspection.State.Running {
			return inspection.ID, nil
		}
		containerID, err := docker.waitForReusableOrReleased(ctx, plan.ProjectRoot, containerName)
		if err != nil {
			return "", err
		}
		if containerID != "" {
			return containerID, nil
		}
	}

	if err := docker.preflight(ctx, image); err != nil {
		return "", err
	}
	for attempt := 0; attempt < 2; attempt++ {
		containerID, conflict, err := docker.createProjectContainer(ctx, plan, image, containerName)
		if err != nil {
			return "", err
		}
		if !conflict {
			return containerID, nil
		}
		containerID, err = docker.waitForReusableOrReleased(ctx, plan.ProjectRoot, containerName)
		if err != nil {
			return "", err
		}
		if containerID != "" {
			return containerID, nil
		}
	}
	return "", fmt.Errorf("container name %q was not released after a concurrent create", containerName)
}

func (docker *Docker) createProjectContainer(
	ctx context.Context,
	plan Plan,
	image string,
	containerName string,
) (string, bool, error) {
	arguments, err := BuildDockerRunArgs(
		plan,
		image,
		containerName,
		docker.HostUID,
		docker.HostGID,
		docker.HostUser,
		docker.HostGroup,
		docker.HostHome,
		docker.HostGitConfig,
	)
	if err != nil {
		return "", false, err
	}
	output, err := docker.Runner.CombinedOutput(ctx, docker.Binary, arguments...)
	if err != nil {
		if isContainerNameConflict(output, err) {
			return "", true, nil
		}
		return "", false, commandFailure("create detached Sysbox container", output, err)
	}
	containerID := strings.TrimSpace(string(output))
	if err := validateContainerID(containerID); err != nil {
		return "", false, fmt.Errorf("parse created Sysbox container: %w", err)
	}
	return containerID, false, nil
}

func (docker *Docker) execProjectCommand(
	ctx context.Context,
	plan Plan,
	command []string,
	containerID string,
) error {
	arguments, err := BuildDockerExecArgs(
		plan,
		command,
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
		arguments,
		docker.Stdin,
		docker.Stdout,
		docker.Stderr,
	); err != nil {
		return fmt.Errorf("exec in managed Sysbox container: %w", err)
	}
	return nil
}

type containerInspection struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
}

func (docker *Docker) inspectProjectContainer(
	ctx context.Context,
	containerName string,
) (containerInspection, bool, error) {
	output, err := docker.Runner.CombinedOutput(ctx, docker.Binary, "container", "inspect", containerName)
	if err != nil {
		if isContainerNotFound(output, err) {
			return containerInspection{}, false, nil
		}
		return containerInspection{}, false, commandFailure(
			fmt.Sprintf("inspect managed container %q", containerName),
			output,
			err,
		)
	}
	var inspections []containerInspection
	if err := json.Unmarshal(output, &inspections); err != nil {
		return containerInspection{}, false, fmt.Errorf("parse managed container inspection: %w", err)
	}
	if len(inspections) != 1 {
		return containerInspection{}, false, fmt.Errorf(
			"inspect managed container %q returned %d records",
			containerName,
			len(inspections),
		)
	}
	if err := validateContainerID(inspections[0].ID); err != nil {
		return containerInspection{}, false, fmt.Errorf("parse managed container inspection: %w", err)
	}
	return inspections[0], true, nil
}

func (docker *Docker) validateProjectContainer(inspection containerInspection, projectRoot string) error {
	expected := map[string]string{
		managedLabel:         managedLabelValue,
		projectPathLabel:     projectRoot,
		hostUIDLabel:         strconv.Itoa(docker.HostUID),
		managerProtocolLabel: session.ProtocolVersion,
	}
	for name, value := range expected {
		if got := inspection.Config.Labels[name]; got != value {
			return fmt.Errorf(
				"container %s has label %s=%q, expected %q; refusing deterministic-name reuse",
				inspection.ID,
				name,
				got,
				value,
			)
		}
	}
	return nil
}

func (docker *Docker) waitForReusableOrReleased(
	ctx context.Context,
	projectRoot string,
	containerName string,
) (string, error) {
	waitContext, cancel := context.WithTimeout(ctx, containerStateTimeout)
	defer cancel()
	ticker := time.NewTicker(containerPollInterval)
	defer ticker.Stop()
	for {
		inspection, found, err := docker.inspectProjectContainer(waitContext, containerName)
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

func (docker *Docker) containerStoppedAfterExec(
	ctx context.Context,
	plan Plan,
	containerName string,
) (bool, error) {
	inspection, found, err := docker.inspectProjectContainer(ctx, containerName)
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
		return false, nil
	}
	containerID, err := docker.waitForReusableOrReleased(ctx, plan.ProjectRoot, containerName)
	return containerID == "" && err == nil, err
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

// BuildDockerRunArgs returns argv for one detached outer-container creation
// without attaching a user command, stdin, or TTY.
func BuildDockerRunArgs(
	plan Plan,
	image string,
	containerName string,
	hostUID int,
	hostGID int,
	hostUser string,
	hostGroup string,
	hostHome string,
	hostGitConfig string,
) ([]string, error) {
	if strings.TrimSpace(image) == "" {
		return nil, errors.New("container image is required")
	}
	if err := validateSessionName(containerName); err != nil {
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
		"--detach",
		"--rm",
	}
	args = append(args,
		"--runtime="+sysboxRuntime,
		"--name",
		containerName,
		"--label",
		managedLabel+"="+managedLabelValue,
		"--label",
		projectPathLabel+"="+plan.ProjectRoot,
		"--label",
		hostUIDLabel+"="+strconv.Itoa(hostUID),
		"--label",
		managerProtocolLabel+"="+session.ProtocolVersion,
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

	return args, nil
}

// BuildDockerExecArgs returns argv for one wrapped command in an already-running project container.
func BuildDockerExecArgs(
	plan Plan,
	command []string,
	containerID string,
	hostUID int,
	hostGID int,
	hostHome string,
	tty bool,
) ([]string, error) {
	if len(command) == 0 {
		return nil, errors.New("command is required")
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
		"codex-safe-session",
		"run",
		"--",
	)
	args = append(args, command...)

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

func isContainerNotFound(output []byte, err error) bool {
	if commandExitCode(err) != 1 {
		return false
	}
	message := strings.ToLower(string(output))
	return strings.Contains(message, "no such container") || strings.Contains(message, "no such object")
}

func isContainerNameConflict(output []byte, err error) bool {
	if commandExitCode(err) != 125 {
		return false
	}
	message := strings.ToLower(string(output))
	return strings.Contains(message, "container name") && strings.Contains(message, "already in use")
}

func commandExitCode(err error) int {
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

func isRetryableExecError(err error) bool {
	var commandError interface{ CommandStderr() string }
	if !errors.As(err, &commandError) {
		return false
	}
	message := strings.ToLower(commandError.CommandStderr())
	return strings.Contains(message, "codex-safe-session: register session command") ||
		strings.Contains(message, "no such container") ||
		strings.Contains(message, "container is not running") ||
		strings.Contains(message, "container is restarting")
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
	if stderr == nil {
		stderr = io.Discard
	}
	capturedStderr := &tailBuffer{limit: 64 * 1024}
	command.Stderr = io.MultiWriter(stderr, capturedStderr)
	if err := command.Run(); err != nil {
		return &dockerCommandError{err: err, stderr: capturedStderr.String()}
	}
	return nil
}

type dockerCommandError struct {
	err    error
	stderr string
}

func (err *dockerCommandError) Error() string {
	return err.err.Error()
}

func (err *dockerCommandError) Unwrap() error {
	return err.err
}

func (err *dockerCommandError) ExitCode() int {
	return commandExitCode(err.err)
}

func (err *dockerCommandError) CommandStderr() string {
	return err.stderr
}

type tailBuffer struct {
	limit int
	data  []byte
}

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if originalLength >= buffer.limit {
		buffer.data = append(buffer.data[:0], data[originalLength-buffer.limit:]...)
		return originalLength, nil
	}
	overflow := len(buffer.data) + originalLength - buffer.limit
	if overflow > 0 {
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:len(buffer.data)-overflow]
	}
	buffer.data = append(buffer.data, data...)
	return originalLength, nil
}

func (buffer *tailBuffer) String() string {
	return string(buffer.data)
}
