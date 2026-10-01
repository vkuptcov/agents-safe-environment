package smoke_test

import (
	"os"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
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
	var storageName string
	for _, mount := range stopped.Mounts {
		if mount.Destination == "/var/lib/docker" {
			require.EqualValues(t, "volume", mount.Type)
			require.True(t, mount.RW)
			storageName = mount.Name
		}
	}
	require.NotEmpty(t, storageName)
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
	for _, mount := range replaced.Mounts {
		if mount.Destination == "/var/lib/docker" {
			require.Equal(t, storageName, mount.Name)
		}
	}
	fixture.docker.waitForContainerRemoval()
}
