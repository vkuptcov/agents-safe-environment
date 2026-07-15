package smoke_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSysboxAgentsSafeBash proves the public generic launcher accepts the documented direct
// `agents-safe bash` form and starts Bash in the same isolated Sysbox environment.
func TestSysboxAgentsSafeBash(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox agents-safe test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	if _, err := os.Stat(fixture.launcher.agentsBinary); err != nil {
		t.Skip("bin/agents-safe is missing; run make build first")
	}

	report := filepath.Join(fixture.project.worktree, "agents-safe-bash.report")
	ready := filepath.Join(fixture.project.worktree, "agents-safe-bash.ready")
	release := filepath.Join(fixture.project.worktree, "agents-safe-bash.release")
	command := fixture.launcher.startAgents(
		fixture.project.worktree,
		"bash", "-c", `printf 'shell=%s\nproject=%s\n' "$0" "$PWD" > "$1"
: > "$2"
while [[ ! -e "$3" ]]; do sleep 1; done`, "bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)

	observed := parseReport(t, report)
	require.Equal(t, "bash", observed["shell"], "agents-safe must execute Bash directly")
	require.Equal(t, fixture.project.worktree, observed["project"], "Bash must start in the selected project")
	require.True(t, fixture.docker.inspectOuter().State.Running, "Bash must keep its managed session alive")
	fixture.release(release, command, "agents-safe Bash command")
	fixture.docker.waitForOuterRemoval()
}
