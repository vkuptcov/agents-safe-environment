package launcher

import (
	"context"
	"fmt"
	"runtime"
	"slices"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// prepareImage runs only after active-session reuse has been ruled out. An explicit image override
// bypasses project discovery; otherwise a discovered context is built and pins the selected image.
func (attempt *launchAttempt) prepareImage(ctx context.Context) error {
	if attempt.imageOverride {
		return attempt.cli.Preflight(ctx, sysboxRuntime, attempt.image)
	}
	contextPath, err := projectenv.Discover(attempt.plan.ProjectRoot)
	if err != nil {
		return err
	}
	if contextPath == "" {
		return attempt.cli.Preflight(ctx, sysboxRuntime, attempt.image)
	}
	return attempt.prepareProjectImage(ctx, contextPath)
}

// prepareProjectImage rebuilds the stable project tag and selects its immutable result. It runs
// only after a deterministic running session has been ruled out, so active sessions retain the
// ordinary image-selection semantics and never trigger a build.
func (attempt *launchAttempt) prepareProjectImage(ctx context.Context, contextPath string) error {
	baseImage := attempt.image
	if err := attempt.cli.Preflight(ctx, sysboxRuntime, baseImage); err != nil {
		return err
	}
	tag := projectenv.LocalImageName(attempt.projectKey)
	if err := attempt.cli.Build(ctx, dockercli.BuildRequest{
		Tag:       tag,
		BaseImage: baseImage,
		Context:   contextPath,
	}, attempt.docker.Stderr); err != nil {
		return err
	}
	inspection, err := attempt.cli.InspectImage(ctx, tag)
	if err != nil {
		return err
	}
	if err := validateProjectImage(inspection); err != nil {
		return err
	}
	attempt.image = inspection.ID
	return nil
}

func validateProjectImage(inspection dockercli.ImageInspection) error {
	if inspection.Architecture != runtime.GOARCH {
		return fmt.Errorf("project image %q architecture %q is incompatible with host %q", inspection.ID, inspection.Architecture, runtime.GOARCH)
	}
	if inspection.Config.User != "" && inspection.Config.User != "root" {
		return fmt.Errorf("project image %q user %q is incompatible with root bootstrap", inspection.ID, inspection.Config.User)
	}
	if !slices.Equal(inspection.Config.Entrypoint, []string{"/usr/bin/tini", "--", "/usr/local/bin/codex-safe-session"}) || !slices.Equal(inspection.Config.Command, []string{"serve"}) {
		return fmt.Errorf("project image %q does not preserve the session entrypoint and serve command", inspection.ID)
	}
	if !slices.Contains(inspection.Config.Environment, "DOCKER_HOST=unix:///var/run/docker.sock") {
		return fmt.Errorf("project image %q does not preserve DOCKER_HOST", inspection.ID)
	}
	return nil
}
