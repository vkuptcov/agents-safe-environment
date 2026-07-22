package launcher

import (
	"context"
	"fmt"
	"io"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
)

const (
	codexUpdateContainerName = "codex-safe-codex-update"
	codexUpdateEntrypoint    = "/usr/local/bin/codex-safe-update"
)

// UpdateCodex runs the image's official-installer wrapper with the shared installation volume
// writable. It intentionally does not construct DockerLauncher: updates need neither Git discovery
// nor the Linux/Sysbox session preflight and therefore also work through Docker Desktop on macOS.
func UpdateCodex(ctx context.Context, image string, stdout, stderr io.Writer) error {
	client := dockercli.New("docker", dockercli.NewProcessRunner())
	return updateCodex(ctx, client, image, stdout, stderr)
}

func updateCodex(
	ctx context.Context,
	client *dockercli.Client,
	image string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	request := dockercli.CreateRequest{
		Image:      image,
		Name:       codexUpdateContainerName,
		Entrypoint: codexUpdateEntrypoint,
		Volumes: []dockercli.VolumeMount{{
			Source: CodexInstallationVolume,
			Target: CodexInstallationRoot,
		}},
	}
	if err := client.RunAttached(ctx, request, nil, stdout, stderr); err != nil {
		return fmt.Errorf("update Codex: %w", err)
	}
	return nil
}
