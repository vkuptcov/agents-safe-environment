package dockercli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Inspect returns one container's transport state. Found is false when Docker reports no such container.
func (client *Client) Inspect(
	ctx context.Context,
	containerName string,
) (inspection ContainerInspection, found bool, err error) {
	output, err := client.combinedOutput(ctx, "container", "inspect", containerName)
	if err != nil {
		if isContainerNotFound(output, err) {
			return ContainerInspection{}, false, nil
		}
		return ContainerInspection{}, false, commandFailure(
			fmt.Sprintf("inspect managed container %q", containerName),
			output,
			err,
		)
	}
	var inspections []ContainerInspection
	if err := json.Unmarshal(output, &inspections); err != nil {
		return ContainerInspection{}, false, fmt.Errorf("parse managed container inspection: %w", err)
	}
	if len(inspections) != 1 {
		return ContainerInspection{}, false, fmt.Errorf(
			"inspect managed container %q returned %d records",
			containerName,
			len(inspections),
		)
	}
	if err := validateContainerID(inspections[0].ID); err != nil {
		return ContainerInspection{}, false, fmt.Errorf("parse managed container inspection: %w", err)
	}
	return inspections[0], true, nil
}

// Preflight verifies that Docker exposes the required runtime and can inspect the requested image.
func (client *Client) Preflight(ctx context.Context, runtime string, image string) error {
	if runtime == "" {
		return errors.New("container runtime is required")
	}
	if strings.TrimSpace(image) == "" {
		return errors.New("container image is required")
	}
	output, err := client.combinedOutput(ctx, "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		return commandFailure("query Docker runtimes", output, err)
	}
	runtimes := make(map[string]json.RawMessage)
	if err := json.Unmarshal(output, &runtimes); err != nil {
		return fmt.Errorf("parse Docker runtimes: %w", err)
	}
	if _, found := runtimes[runtime]; !found {
		return fmt.Errorf("Docker runtime %q is not registered", runtime)
	}
	output, err = client.combinedOutput(ctx, "image", "inspect", image)
	if err != nil {
		return commandFailure(fmt.Sprintf("inspect image %q", image), output, err)
	}
	return nil
}

func isContainerNotFound(output []byte, err error) bool {
	if ExitCode(err) != 1 {
		return false
	}
	message := strings.ToLower(string(output))
	return strings.Contains(message, "no such container") || strings.Contains(message, "no such object")
}
