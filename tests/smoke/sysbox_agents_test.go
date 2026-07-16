package smoke_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
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
	require.True(t, fixture.docker.inspectContainer().State.Running, "Bash must keep its managed session alive")
	fixture.release(release, command, "agents-safe Bash command")
	fixture.docker.waitForContainerRemoval()
}

// TestSysboxAgentsSafeWithoutCodexHome proves the optional policy reaches the real Docker boundary:
// no host Codex-home mount is added and the managed command receives no CODEX_HOME.
func TestSysboxAgentsSafeWithoutCodexHome(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox agents-safe test", goSmokeEnv)
	}
	fixture := newSmokeFixtureWithCodexHome(t, false)

	report := filepath.Join(fixture.project.worktree, "agents-safe-no-codex-home.report")
	ready := filepath.Join(fixture.project.worktree, "agents-safe-no-codex-home.ready")
	release := filepath.Join(fixture.project.worktree, "agents-safe-no-codex-home.release")
	command := fixture.launcher.startAgents(
		fixture.project.worktree,
		"bash", "-c", `printf 'codex_home=%s\n' "${CODEX_HOME-unset}" > "$1"
: > "$2"
while [[ ! -e "$3" ]]; do sleep 1; done`, "bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)

	observed := parseReport(t, report)
	require.Equal(t, "unset", observed["codex_home"], "agents-safe must omit CODEX_HOME when the host source is absent")
	inspection := fixture.docker.inspectContainer()
	require.Equal(t, launcher.CodexHomeAbsent, inspection.Config.Labels["codex-safe.codex-home"])
	for _, mount := range inspection.Mounts {
		require.NotEqual(t, fixture.project.codexHome, mount.Destination,
			"agents-safe must not create a Codex-home bind mount when the source is absent")
		require.False(t, strings.Contains(mount.Source, fixture.project.codexHome),
			"agents-safe must not bind the absent host Codex-home source")
	}

	fixture.release(release, command, "agents-safe command without Codex home")
	fixture.docker.waitForContainerRemoval()
}
