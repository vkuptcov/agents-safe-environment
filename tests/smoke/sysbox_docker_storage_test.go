package smoke_test

import (
	"os"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

// TestSysboxDockerStorageSurvivesReplacement proves persistence across both a kept
// container restart and removal/recreation of the outer container.
func TestSysboxDockerStorageSurvivesReplacement(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 for real Sysbox storage checks", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	first := fixture.launcher.startKept(fixture.project.worktree, "bash", "-c",
		`docker pull "$1" && docker volume create storage-proof && docker run --rm -v storage-proof:/proof "$1" sh -c 'echo persisted > /proof/marker'`, "bash", nestedImage)
	first.requireExit(t, "seed persistent Docker storage")
	stopped := fixture.docker.waitForContainerStop()
	storageName := dockerStorageVolumeName(t, stopped)
	requireVolumeMount(t, stopped, storageName, launchplan.DockerDataRoot, true)
	check := `docker image inspect "$1" >/dev/null && docker run --pull=never --rm -v storage-proof:/proof "$1" cat /proof/marker`
	second := fixture.launcher.start(fixture.project.worktree, "bash", "-c", check, "bash", nestedImage)
	second.requireExit(t, "read Docker storage after kept restart")
	require.Equal(t, "persisted\n", second.stdout.String())
	require.Equal(t, stopped.ID, fixture.docker.inspectContainer().ID)
	fixture.docker.waitForContainerStop()
	require.NoError(t, fixture.docker.client.ContainerRemove(fixture.docker.ctx, fixture.docker.names.managed, container.RemoveOptions{}))
	third := fixture.launcher.start(fixture.project.worktree, "bash", "-c", check, "bash", nestedImage)
	third.requireExit(t, "read Docker storage after outer replacement")
	require.Equal(t, "persisted\n", third.stdout.String())
	replaced := fixture.docker.inspectContainer()
	require.NotEqual(t, stopped.ID, replaced.ID)
	requireVolumeMount(t, replaced, storageName, launchplan.DockerDataRoot, true)
	fixture.docker.waitForContainerRemoval()
}

// dockerStorageVolumeName reports the persistent nested-Docker volume the launcher actually mounted.
func dockerStorageVolumeName(t *testing.T, inspection container.InspectResponse) string {
	t.Helper()
	for _, mount := range inspection.Mounts {
		if mount.Destination == launchplan.DockerDataRoot {
			require.EqualValues(t, "volume", mount.Type)
			return mount.Name
		}
	}
	t.Fatalf("no nested Docker storage volume mounted at %q", launchplan.DockerDataRoot)
	return ""
}
