package smoke_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// TestSysboxProjectEnvironment exercises the public no---image path with a real derived image. The first build is
// created through Docker here because the production launcher deliberately accepts cold builds only from a terminal;
// the separate manual release gate covers that confirmation boundary. This test proves normal cached selection,
// active-session digest rejection, changed-definition selection, labels, and cleanup without adding a PTY dependency.
func TestSysboxProjectEnvironment(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox project-environment test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	if _, err := os.Stat(fixture.launcher.agentsBinary); err != nil {
		t.Skip("bin/agents-safe is missing; run make build first")
	}

	dockerfile := filepath.Join(fixture.project.worktree, projectenv.Directory, projectenv.DockerfileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(dockerfile), 0o755), "fixture project environment directory must be created")
	require.NoError(t, os.WriteFile(dockerfile, []byte(projectEnvironmentDockerfile("v1")), 0o644),
		"fixture project Dockerfile must be written")
	definition := fixture.buildProjectImage(t)

	report := filepath.Join(fixture.project.worktree, "project-environment.report")
	ready := filepath.Join(fixture.project.worktree, "project-environment.ready")
	release := filepath.Join(fixture.project.worktree, "project-environment.release")
	command := fixture.launcher.startDefault(
		fixture.project.worktree,
		"bash", "-c", `set -e; project-tool "$1" "$2"; : > "$3"; while [[ ! -e "$4" ]]; do sleep 1; done`,
		"bash", "v1", report, ready, release,
	)
	fixture.waitForFile(ready, command)
	require.Equal(t, "v1", parseReport(t, report)["project_tool"], "cached project image must supply its tool")
	inspection := fixture.docker.inspectContainer()
	require.Equal(t, definition.EnvironmentLabel(), inspection.Config.Labels["codex-safe.project-environment"],
		"managed session must record the selected project definition")

	require.NoError(t, os.WriteFile(dockerfile, []byte(projectEnvironmentDockerfile("v2")), 0o644),
		"changed fixture project Dockerfile must be written")
	mismatch := fixture.launcher.startDefault(fixture.project.worktree, "true")
	select {
	case <-mismatch.done:
		require.Error(t, mismatch.err, "changed definition must not reuse the active session")
		require.Contains(t, mismatch.stderr.String(), "finish the active session", "mismatch must direct the user to finish it")
	case <-time.After(commandTimeout):
		t.Fatalf("timed out waiting for changed project-environment diagnostic\n%s", mismatch.diagnostics())
	}

	fixture.release(release, command, "cached project-environment command")
	fixture.docker.waitForContainerRemoval()
	changed := fixture.buildProjectImage(t)
	updated := fixture.launcher.startDefault(fixture.project.worktree, "project-tool", "v2", report)
	updated.requireExit(t, "changed cached project-environment command")
	require.Equal(t, "v2", parseReport(t, report)["project_tool"], "changed definition must select its new image")
	require.NotEqual(t, definition.Digest, changed.Digest, "fixture Dockerfile mutation must change the definition digest")
}

func projectEnvironmentDockerfile(version string) string {
	return `ARG AGENTS_SAFE_BASE
FROM ${AGENTS_SAFE_BASE}
RUN printf '%s\n' '#!/bin/sh' 'printf "project_tool=%s\n" "$1" > "$2"' > /usr/local/bin/project-tool && chmod 0755 /usr/local/bin/project-tool
` + "# fixture-version: " + version + "\n"
}

func (fixture *smokeFixture) buildProjectImage(t *testing.T) *projectenv.Definition {
	t.Helper()
	definition, err := projectenv.Discover(fixture.project.worktree)
	require.NoError(t, err, "fixture project definition must be discoverable")
	require.NotNil(t, definition, "fixture project definition must exist")
	base, err := fixture.docker.client.ImageInspect(fixture.docker.ctx, goSmokeImage)
	require.NoError(t, err, "base image must be inspectable")
	cacheKey, err := projectenv.CacheKey(definition.Digest, base.ID)
	require.NoError(t, err, "fixture project cache key must be derived")
	tag, err := projectenv.LocalImageName(launcher.ProjectKey(os.Getuid(), fixture.project.worktree), cacheKey)
	require.NoError(t, err, "fixture project image tag must be derived")
	labels := []string{
		projectenv.ProjectImageLabel + "=" + projectenv.ProjectImageLabelValue,
		projectenv.ProjectKeyLabel + "=" + launcher.ProjectKey(os.Getuid(), fixture.project.worktree),
		projectenv.DefinitionLabel + "=" + definition.Digest,
		projectenv.BaseImageIDLabel + "=" + base.ID,
	}
	arguments := []string{"build", "--file", definition.DockerfilePath, "--tag", tag, "--build-arg", "AGENTS_SAFE_BASE=" + goSmokeImage}
	for _, label := range labels {
		arguments = append(arguments, "--label", label)
	}
	arguments = append(arguments, definition.ContextPath)
	runOutput(t, fixture.project.worktree, "docker", arguments...)
	inspection, err := fixture.docker.client.ImageInspect(fixture.docker.ctx, tag)
	require.NoError(t, err, "fixture project image must be inspectable after build")
	t.Logf("built fixture project image %s (%s)", tag, inspection.ID)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = fixture.docker.client.ContainerRemove(cleanupContext, fixture.docker.names.managed, container.RemoveOptions{Force: true})
		_, _ = fixture.docker.client.ImageRemove(cleanupContext, tag, image.RemoveOptions{Force: true, PruneChildren: true})
	})
	return definition
}
