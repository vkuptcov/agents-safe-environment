package smoke_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSysboxLinkedWorktreeGo exercises the complete real-host Sysbox scenario.
// The launcher is invoked as a real host process and host Docker is accessed
// through Moby; focused embedded workloads exercise the container itself.
func TestSysboxLinkedWorktreeGo(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox smoke test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	fixture.docker.startSentinel()

	environment := fixture.startEnvironmentProbe()
	fixture.waitForFile(fixture.files.environment.ready, environment)
	fixture.assertEnvironment()

	worktree := fixture.startWorktreeProbe()
	worktree.requireExit(t, "worktree probe")
	fixture.assertWorktree()

	nestedDocker := fixture.startNestedDockerProbe()
	fixture.waitForFile(fixture.files.nestedDocker.ready, nestedDocker)
	fixture.assertNestedDocker()
	fixture.assertOuterContainer()

	reused := fixture.startReuseCommand()
	fixture.waitForFile(fixture.files.reuse.report, reused)
	fixture.assertReuse()

	fixture.release(fixture.files.environment.release, environment, "environment command")
	require.True(t, reused.running(), "reused command must survive environment command exit")
	require.True(t, nestedDocker.running(), "nested Docker command must survive environment command exit")
	require.True(t, fixture.docker.inspectContainer().State.Running, "container session must remain running after one command exits")

	fixture.release(fixture.files.nestedDocker.release, nestedDocker, "nested Docker command")
	require.True(t, reused.running(), "reused command must survive nested Docker command exit")
	fixture.release(fixture.files.reuse.release, reused, "reused command")
	require.True(t, fixture.docker.inspectContainer().State.Running, "idle grace period must retain the container session")
	fixture.docker.waitForContainerRemoval()
	fixture.assertAfterNestedDockerCleanup()

	fixture.assertConcurrentCreation()
}

func (fixture *smokeFixture) assertEnvironment() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.files.environment.report)
	require.Equal(fixture.t, fixture.host.user, report["user"], "container user name must match the host")
	require.Equal(fixture.t, fixture.host.group, report["group"], "container primary group must match the host")
	require.Equal(fixture.t, fixture.project.hostHome, report["home"], "container home must preserve the host path")
	require.Equal(fixture.t, fixture.project.hostHome, report["passwd_home"], "passwd home must preserve the host path")
	require.Equal(fixture.t, "0", report["sudo_uid"], "host user must receive passwordless container-root access")
	require.Equal(fixture.t, "440", report["sudoers_mode"], "sudoers policy must have mode 0440")
	require.Equal(fixture.t, "false", report["sudoers_writable"], "host user must not modify its sudoers policy")
	require.Equal(fixture.t, fixture.host.gitMarker, report["git_marker"], "mounted global Git config must be visible")
	require.Equal(fixture.t, "false", report["git_writable"], "mounted global Git config must be read-only")
	require.Equal(fixture.t, "UTF-8", report["locale"], "container locale must support UTF-8")
	colors, err := strconv.Atoi(report["colors"])
	require.NoError(fixture.t, err, "terminal color count must be numeric")
	require.GreaterOrEqual(fixture.t, colors, 256, "terminal must advertise at least 256 colors")
	require.Equal(fixture.t, "true", report["color_prompt"], "interactive Bash prompt must use colors")
	require.Equal(fixture.t, "true", report["color_ls"], "interactive Bash must configure color-aware ls")
	require.Equal(fixture.t, "true", report["tool_less"], "less must be available")
	require.Equal(fixture.t, "true", report["tool_make"], "make must be available")
	require.Equal(fixture.t, "true", report["tool_rg"], "rg must be available")
	require.Equal(fixture.t, "true", report["docker_compose"], "Docker Compose must be available")
	require.Equal(fixture.t, "true", report["make_completion"], "Make completion must be registered")
}

func (fixture *smokeFixture) assertWorktree() {
	fixture.t.Helper()
	require.Equal(fixture.t, "Привет из codex-safe\n", readFile(fixture.t, fixture.files.cyrillic), "Cyrillic project data must round-trip")
	require.Equal(fixture.t, "staged by Sysbox probe\n", readFile(fixture.t, fixture.files.staged), "probe must stage a linked-worktree file")
	require.Equal(fixture.t, "primary baseline\n", readFile(fixture.t, filepath.Join(fixture.project.primary, "baseline.txt")), "primary checkout must remain read-only")
	requireHostOwnership(fixture.t, fixture.project.worktree, fixture.project.primary)
}

func (fixture *smokeFixture) assertNestedDocker() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.files.nestedDocker.report)
	require.NotEmpty(fixture.t, report["nested_daemon"], "nested Docker daemon ID must be present")
	require.NotEqual(fixture.t, fixture.docker.daemonID, report["nested_daemon"], "nested Docker daemon must differ from host Docker")
	require.Equal(fixture.t, "crun", report["nested_runtime"], "nested Docker default runtime must be crun")
	require.Equal(fixture.t, "false", report["sentinel_visible"], "nested Docker must not see a host container")
	require.Equal(fixture.t, "true", report["nested_running"], "nested Docker container must be running")
	require.Equal(fixture.t, "true", report["compose_running"], "nested Compose service must be running")
	require.Equal(fixture.t, "nested marker\n", readFile(fixture.t, fixture.files.nestedMarker), "nested Docker bind mount must write the project")
}

func (fixture *smokeFixture) assertOuterContainer() {
	fixture.t.Helper()
	inspection := fixture.docker.inspectContainer()
	require.True(fixture.t, inspection.State.Running, "container must run while probe command is active")
	require.Equal(fixture.t, "true", inspection.Config.Labels["codex-safe.managed"], "managed label must identify the session")
	require.Equal(fixture.t, fixture.project.worktree, inspection.Config.Labels["codex-safe.project-path"], "project label must name the linked worktree")
	require.Equal(fixture.t, strconv.Itoa(os.Getuid()), inspection.Config.Labels["codex-safe.host-uid"], "host UID label must be present")
	require.Equal(fixture.t, "1", inspection.Config.Labels["codex-safe.manager-protocol"], "manager protocol label must be present")
	require.Equal(fixture.t, fixture.project.nested, inspection.Config.WorkingDir, "container working directory must preserve nested invocation path")
	require.Equal(fixture.t, "sysbox-runc", inspection.HostConfig.Runtime, "container must use Sysbox runtime")
	require.False(fixture.t, inspection.HostConfig.Privileged, "container must not be privileged")
	requireMount(fixture.t, inspection, fixture.project.primary, fixture.project.primary, false)
	requireMount(fixture.t, inspection, filepath.Join(fixture.project.primary, ".git"), filepath.Join(fixture.project.primary, ".git"), true)
	requireMount(fixture.t, inspection, fixture.project.worktree, fixture.project.worktree, true)
	requireMount(fixture.t, inspection, fixture.project.hostGit, fixture.project.hostGit, false)
	for _, mount := range inspection.Mounts {
		require.NotEqual(fixture.t, "/var/run/docker.sock", mount.Source, "host Docker socket must not be mounted")
		require.NotEqual(fixture.t, "/var/run/docker.sock", mount.Destination, "container Docker socket must not be mounted")
	}
	require.Len(fixture.t, fixture.docker.managedContainers(), 1, "there must be exactly one active managed container")
}

func (fixture *smokeFixture) assertReuse() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.files.reuse.report)
	inspection := fixture.docker.inspectContainer()
	require.Equal(fixture.t, inspection.Config.Hostname, report["hostname"], "reused command must run in the same container")
	require.Equal(fixture.t, fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), report["identity"], "reused command must retain host identity")
	require.Equal(fixture.t, fixture.project.worktree, report["pwd"], "reused command must preserve its requested working directory")
	require.NotEmpty(fixture.t, report["nested_daemon"], "reused command must access nested Docker")
	require.Equal(fixture.t, parseReport(fixture.t, fixture.files.nestedDocker.report)["nested_daemon"], report["nested_daemon"], "reused command must retain nested Docker daemon")
	require.Len(fixture.t, fixture.docker.managedContainers(), 1, "reuse must not create a second container")
}

func (fixture *smokeFixture) assertAfterNestedDockerCleanup() {
	fixture.t.Helper()
	require.Empty(fixture.t, fixture.docker.containersNamed(fixture.docker.names.nested), "nested container must never appear in host Docker")
	require.Empty(fixture.t, fixture.docker.containersNamed(fixture.docker.names.compose), "Compose container must never appear in host Docker")
	sentinel, err := fixture.docker.client.ContainerInspect(fixture.docker.ctx, fixture.docker.names.sentinel)
	require.NoError(fixture.t, err, "host sentinel must remain inspectable")
	require.True(fixture.t, sentinel.State.Running, "host sentinel must remain running after nested cleanup")
	staged := runOutput(fixture.t, fixture.project.worktree, "git", "diff", "--cached", "--name-only")
	require.Contains(fixture.t, staged, filepath.Base(fixture.files.staged), "linked-worktree file must remain staged")
	requireHostOwnership(fixture.t, fixture.project.worktree, filepath.Join(fixture.project.primary, ".git"))
	require.Equal(fixture.t, fixture.host.gitConfig, readFile(fixture.t, fixture.project.hostGit), "host Git config must remain unchanged")
	require.NoError(fixture.t, appendFile(fixture.files.nestedMarker, []byte("host edit\n")), "host user must be able to append to the nested-container marker")
	require.Equal(fixture.t, "nested marker\nhost edit\n", readFile(fixture.t, fixture.files.nestedMarker), "host edit of the nested-container marker must persist")
}

func (fixture *smokeFixture) assertConcurrentCreation() {
	fixture.t.Helper()
	marker := filepath.Join(fixture.project.worktree, "concurrent.release")
	first := fixture.launcher.start(fixture.project.worktree, "bash", "-c", waitScript, "bash", marker)
	second := fixture.launcher.start(fixture.project.worktree, "bash", "-c", waitScript, "bash", marker)
	fixture.docker.waitForContainer()
	require.Len(fixture.t, fixture.docker.managedContainers(), 1, "concurrent first callers must create only one container")
	fixture.release(marker, first, "first concurrent caller")
	second.requireExit(fixture.t, "second concurrent caller")
	fixture.docker.waitForContainerRemoval()
}
