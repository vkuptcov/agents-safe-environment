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
//
// Every named-volume mount is required to be read-only here: a detached create request is either the
// session container or its relay sidecar, and the only volume mount either may carry is the read-only
// Codex installation store. The read-write maintenance mount (Phase 5) uses BuildRunAttachedArgs
// instead, which does not carry this restriction.
func BuildCreateArgs(request CreateRequest) ([]string, error) {
	if err := requireReadOnlyVolumeMounts(request.Mounts); err != nil {
		return nil, err
	}
	return buildRunArgs([]string{"run", "--detach", "--rm"}, request)
}

// BuildRunAttachedArgs encodes a typed create request as foreground `docker run` argv, used by the
// attached maintenance-container transport. Unlike BuildCreateArgs, it does not require named-volume
// mounts to be read-only, since the maintenance container is the store's one read-write writer.
func BuildRunAttachedArgs(request CreateRequest) ([]string, error) {
	return buildRunArgs([]string{"run", "--rm"}, request)
}

func requireReadOnlyVolumeMounts(mounts []Mount) error {
	for _, mount := range mounts {
		if mount.Kind == MountKindVolume && !mount.ReadOnly {
			return fmt.Errorf("named-volume mount %q must be read-only in a detached create request", mount.Target)
		}
	}
	return nil
}

func buildRunArgs(head []string, request CreateRequest) ([]string, error) {
	if strings.TrimSpace(request.Image) == "" {
		return nil, errors.New("container image is required")
	}
	if request.Name == "" {
		return nil, errors.New("container name is required")
	}

	args := append([]string{}, head...)
	if request.Runtime != "" {
		args = append(args, "--runtime="+request.Runtime)
	}
	args = append(args, "--name", request.Name)
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
		args = append(args, "--mount", mountArg(mount))
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

// mountArg renders one Mount as a Docker `--mount` value. Bind rendering is byte-for-byte identical
// to the transport's original bind-only behavior; rprivate propagation is a bind-only concept and
// never appears on a volume mount.
func mountArg(mount Mount) string {
	if mount.Kind == MountKindVolume {
		specification := "type=volume,source=" + mount.Source + ",target=" + mount.Target
		if mount.ReadOnly {
			specification += ",readonly"
		}
		return specification
	}
	specification := "type=bind,source=" + mount.Source + ",target=" + mount.Target
	specification += ",bind-propagation=rprivate"
	if mount.ReadOnly {
		specification += ",readonly"
	}
	return specification
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
	return isNameConflictMessage(string(output))
}

// isNameConflictMessage reports whether a Docker error message describes the "container name already
// in use" failure. Both the detached create path (combined output) and the attached run path (wrapped
// stderr tail) route their message through here so the substring contract stays in one place.
func isNameConflictMessage(message string) bool {
	lowered := strings.ToLower(message)
	return strings.Contains(lowered, "container name") && strings.Contains(lowered, "already in use")
}
