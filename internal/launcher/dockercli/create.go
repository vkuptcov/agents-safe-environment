package dockercli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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
func BuildCreateArgs(request CreateRequest) ([]string, error) {
	if strings.TrimSpace(request.Image) == "" {
		return nil, errors.New("container image is required")
	}
	if request.Name == "" {
		return nil, errors.New("container name is required")
	}
	if request.Runtime == "" {
		return nil, errors.New("container runtime is required")
	}
	if request.WorkingDir == "" {
		return nil, errors.New("container working directory is required")
	}

	args := []string{
		"run",
		"--detach",
		"--rm",
		"--runtime=" + request.Runtime,
		"--name",
		request.Name,
	}
	for _, label := range request.Labels {
		args = append(args, "--label", label.Key+"="+label.Value)
	}
	for _, environment := range request.Environment {
		args = append(args, "--env", environment.Key+"="+environment.Value)
	}
	args = append(args, "--workdir", request.WorkingDir)
	for _, mount := range request.Mounts {
		args = append(args, "--mount", bindMountArg(mount))
	}
	return append(args, request.Image), nil
}

func bindMountArg(mount Mount) string {
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
	message := strings.ToLower(string(output))
	return strings.Contains(message, "container name") && strings.Contains(message, "already in use")
}
