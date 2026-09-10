package smoke_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSysboxKeptContainerRestarts proves that --keep-container leaves the session container in place
// after idle shutdown and that the next launch restarts that same container, with its writable layer
// intact, instead of creating a new one.
func TestSysboxKeptContainerRestarts(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox persistent-container test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	const marker = "/var/tmp/agents-safe-kept.marker"

	first := fixture.launcher.startKept(fixture.project.worktree, "bash", "-c", `echo persisted > `+marker)
	first.requireExit(t, "first kept command")
	stopped := fixture.docker.waitForContainerStop()
	require.False(t, stopped.HostConfig.AutoRemove, "a kept session must be created without --rm")
	require.Equal(t, "exited", stopped.State.Status, "a kept session rests in the exited state")

	second := fixture.launcher.start(fixture.project.worktree, "bash", "-c", `cat `+marker)
	second.requireExit(t, "second command in the restarted session")
	require.Equal(t, "persisted\n", second.stdout.String(), "the writable layer must survive the restart\n%s", second.diagnostics())
	require.Contains(t, second.stderr.String(), "restarting persistent session container",
		"the restart must be announced on stderr")
	restarted := fixture.docker.inspectContainer()
	require.Equal(t, stopped.ID, restarted.ID, "the same container must be restarted, not replaced")
	require.True(t, restarted.State.Running, "the restarted session must be running after the command")

	fixture.docker.waitForContainerStop()
}
