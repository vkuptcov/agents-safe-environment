package dockercli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Create starts one detached container. Conflict is true when its deterministic name is already in use.
func (client *Client) Create(ctx context.Context, request CreateRequest) (containerID string, conflict bool, err error) {
	arguments, err := BuildCreateArgs(request)
	if err != nil {
		return "", false, err
	}
	output, err := client.combinedOutput(ctx, arguments...)
	if err != nil {
		if isContainerNameConflict(output, err) {
			return "", true, nil
		}
		return "", false, commandFailure("create detached Sysbox container", output, err)
	}
	containerID = strings.TrimSpace(string(output))
	if err := validateContainerID(containerID); err != nil {
		return "", false, fmt.Errorf("parse created Sysbox container: %w", err)
	}
	return containerID, false, nil
}

// BuildCreateArgs encodes a typed create request as Docker CLI argv.
//
// Runtime and WorkingDir are optional so this one builder can encode the relay sidecar, which takes
// the Docker default runtime and has no project directory. That relaxation removes the mechanism
// that previously made a session container falling off sysbox-runc impossible, so the session's own
// creation path enforces the explicit-runtime invariant instead.
func BuildCreateArgs(request CreateRequest) ([]string, error) {
	if request.Name == "" {
		return nil, errors.New("container name is required")
	}
	return buildRunArgs([]string{"run", "--detach", "--rm"}, request)
}

// BuildRunAttachedArgs encodes a typed create request as foreground `docker run` argv, used by the
// attached maintenance-container transport.
func BuildRunAttachedArgs(request CreateRequest) ([]string, error) {
	return buildRunArgs([]string{"run", "--rm"}, request)
}

func buildRunArgs(head []string, request CreateRequest) ([]string, error) {
	if strings.TrimSpace(request.Image) == "" {
		return nil, errors.New("container image is required")
	}

	args := append([]string{}, head...)
	if request.Runtime != "" {
		args = append(args, "--runtime="+request.Runtime)
	}
	if request.Name != "" {
		args = append(args, "--name", request.Name)
	}
	if request.User != "" {
		args = append(args, "--user", request.User)
	}
	if request.NetworkMode != "" {
		args = append(args, "--network="+request.NetworkMode)
	}
	if request.ReadOnlyRootfs {
		args = append(args, "--read-only")
	}
	for _, capability := range request.CapDrop {
		args = append(args, "--cap-drop="+capability)
	}
	for _, option := range request.SecurityOpt {
		args = append(args, "--security-opt="+option)
	}
	for _, label := range request.Labels {
		args = append(args, "--label", label.Key+"="+label.Value)
	}
	for _, environment := range request.Environment {
		args = append(args, "--env", environment.Key+"="+environment.Value)
	}
	if request.WorkingDir != "" {
		args = append(args, "--workdir", request.WorkingDir)
	}
	for _, mount := range request.Mounts {
		args = append(args, "--mount", bindMountArg(mount))
	}
	for _, mount := range request.Tmpfs {
		args = append(args, "--tmpfs", tmpfsMountArg(mount))
	}
	for _, volume := range request.Volumes {
		args = append(args, "--mount", volumeMountArg(volume))
	}
	if request.Entrypoint != "" {
		args = append(args, "--entrypoint", request.Entrypoint)
	}
	args = append(args, request.Image)
	// An empty command preserves the image's default; the sidecar sets it to select relay.
	return append(args, request.Command...), nil
}

// Stop requests graceful termination of a container and waits for Docker to report it stopped. A
// container Docker no longer knows about is already stopped, so that is success rather than an
// error: the caller wanted it gone and it is.
func (client *Client) Stop(ctx context.Context, name string, timeout time.Duration) error {
	seconds := int(timeout.Round(time.Second).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	output, err := client.combinedOutput(ctx, "stop", "--timeout", strconv.Itoa(seconds), name)
	if err != nil {
		if isContainerNotFound(output, err) {
			return nil
		}
		return commandFailure(fmt.Sprintf("stop container %q", name), output, err)
	}
	return nil
}

func bindMountArg(mount Mount) string {
	specification := "type=bind,source=" + mount.Source + ",target=" + mount.Target
	specification += ",bind-propagation=rprivate"
	if mount.ReadOnly {
		specification += ",readonly"
	}
	return specification
}

func volumeMountArg(mount VolumeMount) string {
	specification := "type=volume,source=" + mount.Source + ",target=" + mount.Target
	if mount.ReadOnly {
		specification += ",readonly"
	}
	return specification
}

// tmpfsMountArg spells out the confinement options instead of relying on Docker's `--tmpfs`
// defaults, which mark every such mount `noexec`. A virtual-environment mask must map its native
// extension modules executable, so it opts out of that one flag while keeping the rest.
func tmpfsMountArg(mount TmpfsMount) string {
	options := []string{"rw", "nosuid", "nodev", "noexec"}
	if mount.Exec {
		options[len(options)-1] = "exec"
	}
	if mount.Mode != "" {
		options = append(options, "mode="+mount.Mode)
	}
	return mount.Target + ":" + strings.Join(options, ",")
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

func isContainerNameConflict(output []byte, err error) bool {
	if ExitCode(err) != 125 {
		return false
	}
	lowered := strings.ToLower(string(output))
	return strings.Contains(lowered, "container name") && strings.Contains(lowered, "already in use")
}
