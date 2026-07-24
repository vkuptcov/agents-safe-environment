package smoke_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
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

// TestSysboxRegularCheckoutNormalizesProjectRoles proves the regular-checkout roles collapse to
// their minimal Docker representation at the real Sysbox boundary.
func TestSysboxRegularCheckoutNormalizesProjectRoles(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox regular-checkout test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	fixture.docker.selectProject(fixture.project.primary)
	ready := filepath.Join(fixture.project.primary, "regular-checkout.ready")
	release := filepath.Join(fixture.project.primary, "regular-checkout.release")
	command := fixture.launcher.startAgents(
		fixture.project.primary,
		"bash", "-c", `: > "$1"; while [[ ! -e "$2" ]]; do sleep 1; done`, "bash", ready, release,
	)
	fixture.waitForFile(ready, command)

	inspection := fixture.docker.inspectContainer()
	requireMount(t, inspection, fixture.project.primary, fixture.project.primary, true)
	for _, mount := range inspection.Mounts {
		require.NotEqual(t, filepath.Join(fixture.project.primary, ".git"), mount.Source,
			"regular checkout must not retain a redundant common-Git bind")
	}
	require.Regexp(t, "^[a-f0-9]{64}$", inspection.Config.Labels["agents-safe.launch-config"],
		"session must persist the normalized creation fingerprint")

	fixture.release(release, command, "regular checkout command")
	fixture.docker.waitForContainerRemoval()
}

// TestSysboxProjectVenvIsIsolated proves a root Python marker reserves .venv before it exists and discovered nested
// environments cannot read or modify their host directories.
func TestSysboxProjectVenvIsIsolated(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox project virtual-environment test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	hostVenv := filepath.Join(fixture.project.worktree, ".venv")
	hostServiceVenv := filepath.Join(fixture.project.worktree, "service", ".venv-dev")
	hostSentinel := filepath.Join(hostVenv, "host-sentinel")
	hostServiceSentinel := filepath.Join(hostServiceVenv, "host-sentinel")
	containerSentinel := filepath.Join(hostVenv, "container-sentinel")
	containerServiceSentinel := filepath.Join(hostServiceVenv, "container-sentinel")
	pythonMarker := filepath.Join(fixture.project.worktree, "pyproject.toml")
	report := filepath.Join(fixture.project.worktree, "project-venv.report")
	ready := filepath.Join(fixture.project.worktree, "project-venv.ready")
	release := filepath.Join(fixture.project.worktree, "project-venv.release")
	require.NoError(t, os.WriteFile(pythonMarker, []byte("[project]\nname = \"smoke\"\nversion = \"0\"\n"), 0o600),
		"root Python project marker must exist")
	require.NoError(t, os.MkdirAll(hostServiceVenv, 0o755), "nested host virtual environment directory must exist")
	require.NoError(t, os.WriteFile(
		filepath.Join(hostServiceVenv, "pyvenv.cfg"), []byte("home = /usr/bin\n"), 0o600,
	), "nested host virtual environment marker must exist")
	require.NoError(t, os.WriteFile(hostServiceSentinel, []byte("host-service\n"), 0o600),
		"nested host virtual environment sentinel must exist")
	_, err := os.Lstat(hostVenv)
	require.True(t, os.IsNotExist(err), "root .venv must not exist before launch")

	command := fixture.launcher.startAgents(
		fixture.project.worktree,
		"bash", "-c", `test -d .venv
test ! -e service/.venv-dev/host-sentinel
printf 'container\n' > .venv/container-sentinel
printf 'container-service\n' > service/.venv-dev/container-sentinel
printf 'isolated=yes\nroot_fstype=%s\nservice_fstype=%s\nroot_exec=%s\nservice_exec=%s\n' \
  "$(stat -f -c %T .venv)" \
  "$(stat -f -c %T service/.venv-dev)" \
  "$(findmnt -no OPTIONS --target .venv | grep -qw noexec && echo no || echo yes)" \
  "$(findmnt -no OPTIONS --target service/.venv-dev | grep -qw noexec && echo no || echo yes)" > "$1"
: > "$2"
while [[ ! -e "$3" ]]; do sleep 1; done`, "bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)
	observed := parseReport(t, report)
	require.Equal(t, "yes", observed["isolated"])
	require.Equal(t, "tmpfs", observed["root_fstype"], "root venv mask must be the effective filesystem")
	require.Equal(t, "tmpfs", observed["service_fstype"], "nested venv mask must be the effective filesystem")
	require.Equal(t, "yes", observed["root_exec"], "root venv mask must be executable so native extensions load")
	require.Equal(t, "yes", observed["service_exec"], "nested venv mask must be executable so native extensions load")
	hostInfo, err := os.Lstat(hostVenv)
	require.NoError(t, err, "cold create must leave the root .venv mountpoint on the host")
	require.True(t, hostInfo.IsDir(), "root .venv mountpoint must be a directory")
	require.Equal(t, "host-service\n", readFile(t, hostServiceSentinel),
		"container must not replace nested host virtual-environment content")
	_, err = os.Stat(containerSentinel)
	require.True(t, os.IsNotExist(err), "container .venv writes must not reach the host")
	_, err = os.Stat(containerServiceSentinel)
	require.True(t, os.IsNotExist(err), "container nested virtual-environment writes must not reach the host")

	fixture.release(release, command, "project virtual-environment command")
	fixture.docker.waitForContainerRemoval()
	require.Equal(t, "host-service\n", readFile(t, hostServiceSentinel),
		"nested host virtual environment must remain unchanged after session removal")
	_, err = os.Stat(hostSentinel)
	require.True(t, os.IsNotExist(err), "proactive host .venv mountpoint must remain empty")
	_, err = os.Stat(containerSentinel)
	require.True(t, os.IsNotExist(err), "container .venv must disappear with the session")
	_, err = os.Stat(containerServiceSentinel)
	require.True(t, os.IsNotExist(err), "container nested virtual environment must disappear with the session")
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
	require.Equal(t, "absent", inspection.Config.Labels["agents-safe.codex-home"])
	for _, mount := range inspection.Mounts {
		require.NotEqual(t, fixture.project.codexHome, mount.Destination,
			"agents-safe must not create a Codex-home bind mount when the source is absent")
		require.False(t, strings.Contains(mount.Source, fixture.project.codexHome),
			"agents-safe must not bind the absent host Codex-home source")
	}

	fixture.release(release, command, "agents-safe command without Codex home")
	fixture.docker.waitForContainerRemoval()
}

// TestSysboxConfiguredMount proves typed local config adds an external read-write directory even when an
// explicit image bypasses project Dockerfile discovery.
func TestSysboxConfiguredMount(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox configured-mount test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	external := filepath.Join(fixture.project.root, "configured mount")
	require.NoError(t, os.Mkdir(external, 0o755), "configured mount source must be created")
	require.NoError(t, os.WriteFile(filepath.Join(external, "input"), []byte("from-host\n"), 0o600),
		"configured mount input must be written")
	contextPath := filepath.Join(fixture.project.worktree, projectenv.Directory)
	require.NoError(t, os.Mkdir(contextPath, 0o755), "project environment directory must be created")
	project, err := gitproject.Discover(t.Context(), fixture.project.worktree)
	require.NoError(t, err, "fixture Git project must be discoverable")
	config, err := launcher.DefaultProjectConfig(project, launcher.HostEnvironment{
		HomeDir: fixture.project.hostHome, GitConfig: fixture.project.hostGit, CodexHome: fixture.project.codexHome,
	}, goSmokeImage)
	require.NoError(t, err, "fixture defaults must be serializable")
	config.Common.Mounts = append(config.Common.Mounts, projectenv.MountConfig{
		Role: projectenv.RoleAdditional, Source: external, Target: external,
	})
	var encoded bytes.Buffer
	require.NoError(t, projectenv.Encode(config, &encoded), "fixture config must encode")
	require.NoError(t, os.WriteFile(
		filepath.Join(contextPath, projectenv.ConfigName),
		encoded.Bytes(),
		0o600,
	), "configured mount file must be written")

	report := filepath.Join(fixture.project.worktree, "configured-mount.report")
	ready := filepath.Join(fixture.project.worktree, "configured-mount.ready")
	release := filepath.Join(fixture.project.worktree, "configured-mount.release")
	command := fixture.launcher.startAgents(
		fixture.project.worktree,
		"bash", "-c", `cat "$1/input" > "$2"; printf 'from-container\n' > "$1/output"; : > "$3";
while [[ ! -e "$4" ]]; do sleep 1; done`, "bash", external, report, ready, release,
	)
	fixture.waitForFile(ready, command)
	require.Equal(t, "from-host\n", readFile(t, report), "container must read the configured mount")
	require.Equal(t, "from-container\n", readFile(t, filepath.Join(external, "output")),
		"container must write the configured mount")
	requireMount(t, fixture.docker.inspectContainer(), external, external, true)

	fixture.release(release, command, "configured-mount command")
	fixture.docker.waitForContainerRemoval()
	requireHostOwnership(t, external)
}
