package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const projectEnvironmentLabel = "codex-safe.project-environment"

// prepareProjectImage resolves, reuses, or builds the selected project definition. It runs only
// after a deterministic running session has been ruled out, so a reusable session never prompts,
// pulls, builds, inspects an image, or probes project-controlled binaries.
func (attempt *launchAttempt) prepareProjectImage(ctx context.Context) error {
	if attempt.definition == nil {
		return nil
	}
	if err := attempt.cli.Preflight(ctx, sysboxRuntime, attempt.baseImage); err != nil {
		return err
	}
	baseID, err := attempt.cli.ResolveImageID(ctx, attempt.baseImage)
	if err != nil {
		return err
	}
	baseInspection, found, err := attempt.cli.InspectImage(ctx, baseID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("base image %q disappeared after preflight", attempt.baseImage)
	}
	cacheKey, err := projectenv.CacheKey(attempt.definition.Digest, baseID)
	if err != nil {
		return err
	}
	tag, err := projectenv.LocalImageName(attempt.projectKey, cacheKey)
	if err != nil {
		return err
	}
	inspection, found, err := attempt.cli.InspectImage(ctx, tag)
	if err != nil {
		return err
	}
	if found && attempt.projectImageCompatible(ctx, inspection, baseInspection.Architecture, baseID) == nil {
		attempt.image = inspection.ID
		return nil
	}
	if err := attempt.confirmProjectBuild(); err != nil {
		return err
	}
	if err := attempt.cli.Build(ctx, dockercli.BuildRequest{
		Dockerfile: attempt.definition.DockerfilePath,
		Tag:        tag,
		BaseImage:  attempt.baseImage,
		Labels:     attempt.projectImageLabels(baseID),
		Context:    attempt.definition.ContextPath,
	}, attempt.docker.Stderr); err != nil {
		return err
	}
	// The build context and mutable base tag are both host-side inputs. Do not attach a label that
	// describes bytes or a base different from what Docker could have read during the build.
	reloaded, err := projectenv.Discover(attempt.plan.ProjectRoot)
	if err != nil {
		return err
	}
	if reloaded == nil || reloaded.Digest != attempt.definition.Digest {
		return errors.New("project environment changed while its image was being built; retry the launch")
	}
	resolvedAgain, err := attempt.cli.ResolveImageID(ctx, attempt.baseImage)
	if err != nil {
		return err
	}
	if resolvedAgain != baseID {
		return fmt.Errorf("base image %q changed while project image was being built; retry the launch", attempt.baseImage)
	}
	inspection, found, err = attempt.cli.InspectImage(ctx, tag)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("project image %q was not found after build", tag)
	}
	if err := attempt.projectImageCompatible(ctx, inspection, baseInspection.Architecture, baseID); err != nil {
		return err
	}
	attempt.image = inspection.ID
	return nil
}

func (attempt *launchAttempt) confirmProjectBuild() error {
	if !attempt.docker.CanPrompt {
		return errors.New("project environment needs a Docker build, but confirmation is not interactive")
	}
	fmt.Fprintf(attempt.docker.Stderr, "Project defines a custom environment:\n  %s\nBuild it with the host Docker daemon? [y/N] ", attempt.definition.DockerfilePath)
	line, err := readPromptLine(attempt.docker.Stdin)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read project-environment confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return errors.New("project environment build was not confirmed")
	}
}

func (attempt *launchAttempt) projectImageLabels(baseID string) []dockercli.KeyValue {
	return []dockercli.KeyValue{
		{Key: projectenv.ProjectImageLabel, Value: projectenv.ProjectImageLabelValue},
		{Key: projectenv.ProjectKeyLabel, Value: attempt.projectKey},
		{Key: projectenv.DefinitionLabel, Value: attempt.definition.Digest},
		{Key: projectenv.BaseImageIDLabel, Value: baseID},
	}
}

func (attempt *launchAttempt) projectImageCompatible(
	ctx context.Context,
	inspection dockercli.ImageInspection,
	baseArchitecture string,
	baseID string,
) error {
	labels := inspection.Config.Labels
	for _, want := range attempt.projectImageLabels(baseID) {
		if labels[want.Key] != want.Value {
			return fmt.Errorf("project image %q has %s=%q, expected %q", inspection.ID, want.Key, labels[want.Key], want.Value)
		}
	}
	if inspection.Architecture != baseArchitecture || inspection.Architecture != runtime.GOARCH {
		return fmt.Errorf("project image %q architecture %q is incompatible with base %q and host %q", inspection.ID, inspection.Architecture, baseArchitecture, runtime.GOARCH)
	}
	if inspection.Config.User != "" && inspection.Config.User != "root" {
		return fmt.Errorf("project image %q user %q is incompatible with root bootstrap", inspection.ID, inspection.Config.User)
	}
	if !slices.Equal(inspection.Config.Entrypoint, []string{"/usr/bin/tini", "--", "/usr/local/bin/codex-safe-session"}) || !slices.Equal(inspection.Config.Command, []string{"serve"}) {
		return fmt.Errorf("project image %q does not preserve the session entrypoint and serve command", inspection.ID)
	}
	if !containsString(inspection.Config.Environment, "DOCKER_HOST=unix:///var/run/docker.sock") {
		return fmt.Errorf("project image %q does not preserve DOCKER_HOST", inspection.ID)
	}
	probeContext, cancel := context.WithTimeout(ctx, projectImageProbeTimeout)
	defer cancel()
	return attempt.cli.Probe(probeContext, dockercli.ProbeRequest{
		ImageID: inspection.ID, Entrypoint: "/bin/sh", Arguments: []string{"-c", "command -v tini && command -v codex-safe-session && command -v codex && command -v docker"},
	}, attempt.docker.Stderr)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
