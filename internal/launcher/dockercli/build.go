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

// BuildArgs encodes a project-image build from its already validated context.
func BuildArgs(request BuildRequest) ([]string, error) {
	for _, value := range []struct{ name, value string }{
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
		"--tag", request.Tag,
		"--build-arg", "AGENTS_SAFE_BASE=" + request.BaseImage,
	}
	return append(args, request.Context), nil
}
