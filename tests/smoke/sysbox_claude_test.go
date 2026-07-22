package smoke_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

// TestSysboxClaudeAndCodexShareOneSession proves both product launchers attach to the same held
// Sysbox container. Claude runs from its read-only managed volume and persists user state through
// the native split ~/.claude and ~/.claude.json mounts; Codex then reuses the same container.
func TestSysboxClaudeAndCodexShareOneSession(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox Claude test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	if _, err := os.Stat(fixture.launcher.claudeBinary); err != nil {
		t.Skip("bin/claude-safe is missing; run make build first")
	}
	if _, err := os.Stat(fixture.launcher.productBinary); err != nil {
		t.Skip("bin/codex-safe is missing; run make build first")
	}

	holdRelease := filepath.Join(fixture.project.worktree, "agents-hold.release")
	hold := fixture.launcher.start(fixture.project.worktree, "bash", "-c", waitScript, "bash", holdRelease)
	fixture.docker.waitForContainer()

	inspection := fixture.docker.inspectContainer()
	initialContainerID := inspection.ID
	requireMount(t, inspection, fixture.project.claudeHome, fixture.project.claudeHome, true)
	requireMount(t, inspection, fixture.project.claudeConfig, fixture.project.claudeConfig, true)
	requireVolumeMount(t, inspection, launcher.ClaudeInstallationVolume, launcher.ClaudeInstallationRoot, false)
	requireVolumeMount(t, inspection, launcher.CodexInstallationVolume, launcher.CodexInstallationRoot, false)
	require.Equal(t, fixture.project.claudeHome, inspection.Config.Labels["codex-safe.claude-home"])
	require.Equal(t, fixture.project.claudeConfig, inspection.Config.Labels["codex-safe.claude-config"])

	claudeVersion := fixture.launcher.startBinary(
		fixture.launcher.claudeBinary, fixture.project.worktree, true, nil, "--version",
	)
	claudeVersion.requireExit(t, "claude-safe version")
	require.Contains(t, claudeVersion.stdout.String()+claudeVersion.stderr.String(), "Claude Code",
		"claude-safe must run the managed Claude Code executable")
	require.Equal(t, initialContainerID, fixture.docker.inspectContainer().ID,
		"claude-safe must reuse the held session")

	claudeState := fixture.launcher.startBinary(
		fixture.launcher.claudeBinary,
		fixture.project.worktree,
		true,
		nil,
		"mcp", "add-json", "safe-smoke", `{"type":"http","url":"https://example.com/mcp"}`, "--scope", "user",
	)
	claudeState.requireExit(t, "claude-safe user-state write")
	var config struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(readFile(t, fixture.project.claudeConfig)), &config),
		"Claude global config must remain valid JSON")
	require.Contains(t, config.MCPServers, "safe-smoke", "Claude must persist state through the host mount")
	require.Equal(t, initialContainerID, fixture.docker.inspectContainer().ID,
		"Claude state writes must stay in the held session")

	codexDoctor := fixture.launcher.startBinary(
		fixture.launcher.productBinary, fixture.project.worktree, true, nil, "doctor",
	)
	codexDoctor.waitDone(t, "codex-safe doctor")
	require.Contains(t, codexDoctor.stdout.String()+codexDoctor.stderr.String(), launcher.CodexBinaryPath,
		"codex-safe must still use the managed Codex executable even when doctor reports missing credentials")
	require.Equal(t, initialContainerID, fixture.docker.inspectContainer().ID,
		"codex-safe must reuse the same session as claude-safe")
	require.True(t, hold.running(), "the shared session must remain active while product commands exit")
	require.Len(t, fixture.docker.managedContainers(), 1, "both product launchers must share one managed container")

	require.NoError(t, os.WriteFile(holdRelease, nil, 0o600), "hold release marker must be writable")
	hold.requireExit(t, "shared agents hold command")
	fixture.docker.waitForContainerRemoval()
	requireHostOwnership(t, fixture.project.claudeHome, fixture.project.claudeConfig)
}
