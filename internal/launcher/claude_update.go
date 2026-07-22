package launcher

import (
	"context"
	"fmt"
	"io"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
)

const claudeUpdateEntrypoint = "/usr/local/bin/claude-safe-update"

// UpdateClaude runs the image's official-installer wrapper with the shared Claude installation
// volume mounted read-write. It deliberately uses ordinary Docker and no project/session state.
func UpdateClaude(ctx context.Context, image string, stdout, stderr io.Writer) error {
	client := dockercli.New("docker", dockercli.NewProcessRunner())
	return updateClaude(ctx, client, image, stdout, stderr)
}

func updateClaude(
	ctx context.Context,
	client *dockercli.Client,
	image string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	request := dockercli.CreateRequest{
		Image:      image,
		Entrypoint: claudeUpdateEntrypoint,
		Volumes: []dockercli.VolumeMount{{
			Source: ClaudeInstallationVolume,
			Target: ClaudeInstallationRoot,
		}},
	}
	if err := client.RunAttached(ctx, request, nil, stdout, stderr); err != nil {
		return fmt.Errorf("update Claude Code: %w", err)
	}
	return nil
}
