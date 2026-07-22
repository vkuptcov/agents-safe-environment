package smoke_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// TestSysboxProjectEnvironment proves the public no---image path builds through production
// orchestration, reuses an active session after the Dockerfile changes, and rebuilds the stable tag
// through BuildKit after that session exits.
func TestSysboxProjectEnvironment(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox project-environment test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	if _, err := os.Stat(fixture.launcher.agentsBinary); err != nil {
		t.Skip("bin/agents-safe is missing; run make build first")
	}

	tag := projectenv.LocalImageName(launcher.ProjectKey(os.Getuid(), fixture.project.worktree))
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_, _ = fixture.docker.client.ImageRemove(
			cleanupContext,
			tag,
			image.RemoveOptions{Force: true, PruneChildren: true},
		)
	})

	dockerfile := filepath.Join(fixture.project.worktree, projectenv.Directory, projectenv.DockerfileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(dockerfile), 0o755),
		"fixture project environment directory must be created")
	require.NoError(t, os.WriteFile(dockerfile, []byte(projectEnvironmentDockerfile("v1")), 0o644),
		"fixture project Dockerfile must be written")

	report := filepath.Join(fixture.project.worktree, "project-environment.report")
	ready := filepath.Join(fixture.project.worktree, "project-environment.ready")
	release := filepath.Join(fixture.project.worktree, "project-environment.release")
	command := fixture.launcher.startDefault(
		fixture.project.worktree,
		"bash", "-c", `set -e; project-tool "$1"; : > "$2"; while [[ ! -e "$3" ]]; do sleep 1; done`,
		"bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)
	require.Equal(t, "v1", parseReport(t, report)["project_tool"], "cold launch must build the project image")

	require.NoError(t, os.WriteFile(dockerfile, []byte(projectEnvironmentDockerfile("v2")), 0o644),
		"changed fixture project Dockerfile must be written")
	activeReport := filepath.Join(fixture.project.worktree, "project-environment-active.report")
	active := fixture.launcher.startDefault(fixture.project.worktree, "project-tool", activeReport)
	active.requireExit(t, "active project-environment reuse")
	require.Equal(t, "v1", parseReport(t, activeReport)["project_tool"],
		"an active session must retain the image it started with")

	fixture.release(release, command, "project-environment command")
	fixture.docker.waitForContainerRemoval()
	updated := fixture.launcher.startDefault(fixture.project.worktree, "project-tool", report)
	updated.requireExit(t, "changed project-environment command")
	require.Equal(t, "v2", parseReport(t, report)["project_tool"],
		"the next cold launch must let BuildKit rebuild the stable project tag")
}

func projectEnvironmentDockerfile(version string) string {
	return `ARG AGENTS_SAFE_BASE=agents-safe-mvp:local
FROM ${AGENTS_SAFE_BASE}
RUN printf '%s\n' '#!/bin/sh' 'printf "project_tool=` + version + `\n" > "$1"' > /usr/local/bin/project-tool && chmod 0755 /usr/local/bin/project-tool
`
}
