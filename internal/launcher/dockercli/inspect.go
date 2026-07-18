package dockercli

import (
	"context"
	"encoding/hex"
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

// Preflight verifies that Docker exposes the required runtime and that the requested image is
// present locally, pulling it when it is not.
//
// The pull is not a convenience. Resolving a reference to an immutable content ID requires the image
// to be local, and this check runs before any create, so without it a reference absent from local
// storage fails preflight and never reaches a pull at all.
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
	return client.ensureImage(ctx, image)
}

// ensureImage makes the reference present locally, pulling only when it is absent.
func (client *Client) ensureImage(ctx context.Context, image string) error {
	if _, err := client.combinedOutput(ctx, "image", "inspect", image); err == nil {
		return nil
	}
	output, err := client.combinedOutput(ctx, "pull", image)
	if err != nil {
		return commandFailure(fmt.Sprintf("pull image %q", image), output, err)
	}
	if output, err := client.combinedOutput(ctx, "image", "inspect", image); err != nil {
		return commandFailure(fmt.Sprintf("inspect image %q after pull", image), output, err)
	}
	return nil
}

// ResolveImageID returns the immutable content ID of a locally present reference.
//
// The session container and its relay sidecar implement one private protocol, so a compatible tag is
// not enough: both must be created from this exact ID, even when the user supplied a mutable tag
// that moves underneath them.
func (client *Client) ResolveImageID(ctx context.Context, image string) (string, error) {
	if strings.TrimSpace(image) == "" {
		return "", errors.New("container image is required")
	}
	output, err := client.combinedOutput(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return "", commandFailure(fmt.Sprintf("resolve image %q to an immutable ID", image), output, err)
	}
	imageID := strings.TrimSpace(string(output))
	if err := validateImageID(imageID); err != nil {
		return "", err
	}
	return imageID, nil
}

// InspectImage returns the config needed to validate a freshly built project image.
func (client *Client) InspectImage(ctx context.Context, image string) (ImageInspection, error) {
	if strings.TrimSpace(image) == "" {
		return ImageInspection{}, errors.New("container image is required")
	}
	output, err := client.combinedOutput(ctx, "image", "inspect", image)
	if err != nil {
		return ImageInspection{}, commandFailure(fmt.Sprintf("inspect image %q", image), output, err)
	}
	var inspections []ImageInspection
	if err := json.Unmarshal(output, &inspections); err != nil {
		return ImageInspection{}, fmt.Errorf("parse image inspection: %w", err)
	}
	if len(inspections) != 1 {
		return ImageInspection{}, fmt.Errorf("inspect image %q returned %d records", image, len(inspections))
	}
	if err := validateImageID(inspections[0].ID); err != nil {
		return ImageInspection{}, fmt.Errorf("parse image inspection: %w", err)
	}
	return inspections[0], nil
}

// validateImageID rejects anything that is not a digest-form content ID, so a malformed value can
// never be passed to a create as if it pinned the image.
func validateImageID(imageID string) error {
	digest, found := strings.CutPrefix(imageID, "sha256:")
	if !found || len(digest) != 64 {
		return fmt.Errorf("invalid Docker image ID %q", imageID)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("invalid Docker image ID %q", imageID)
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
