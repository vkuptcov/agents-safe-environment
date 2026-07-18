package dockercli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Build runs one project-image build and forwards both Docker output streams to diagnostics.
func (client *Client) Build(ctx context.Context, request BuildRequest, diagnostics io.Writer) error {
	arguments, err := BuildArgs(request)
	if err != nil {
		return err
	}
	if err := client.run(ctx, arguments, nil, diagnostics, diagnostics); err != nil {
		return fmt.Errorf("build project image %q: %w", request.Tag, err)
	}
	return nil
}

// BuildArgs encodes a project-image build without widening its already validated context.
func BuildArgs(request BuildRequest) ([]string, error) {
	for _, value := range []struct{ name, value string }{
		{"Dockerfile", request.Dockerfile},
		{"image tag", request.Tag},
		{"base image", request.BaseImage},
		{"build context", request.Context},
	} {
		if strings.TrimSpace(value.value) == "" {
			return nil, errors.New(value.name + " is required")
		}
	}
	args := []string{
		"build",
		"--file", request.Dockerfile,
		"--tag", request.Tag,
		"--build-arg", "AGENTS_SAFE_BASE=" + request.BaseImage,
	}
	for _, label := range request.Labels {
		if label.Key == "" {
			return nil, errors.New("build label key is required")
		}
		args = append(args, "--label", label.Key+"="+label.Value)
	}
	return append(args, request.Context), nil
}

// Probe runs one executable in a mount-free, network-free, read-only container. Callers provide a
// bounded context so a project image can never make this compatibility check run indefinitely.
func (client *Client) Probe(
	ctx context.Context,
	request ProbeRequest,
	diagnostics io.Writer,
) error {
	arguments, err := BuildProbeArgs(request)
	if err != nil {
		return err
	}
	if err := client.run(ctx, arguments, nil, diagnostics, diagnostics); err != nil {
		return fmt.Errorf("probe project image %q: %w", request.ImageID, err)
	}
	return nil
}

// BuildProbeArgs encodes the non-negotiable confinement of a derived-image compatibility probe.
func BuildProbeArgs(request ProbeRequest) ([]string, error) {
	if err := validateImageID(request.ImageID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Entrypoint) == "" {
		return nil, errors.New("probe entrypoint is required")
	}
	if len(request.Arguments) == 0 {
		return nil, errors.New("probe arguments are required")
	}
	return append([]string{
		"run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL",
		"--entrypoint", request.Entrypoint, request.ImageID,
	}, request.Arguments...), nil
}
