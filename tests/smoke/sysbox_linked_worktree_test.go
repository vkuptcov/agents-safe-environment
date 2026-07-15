package smoke_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSysboxLinkedWorktreeGo is the Go equivalent of sysbox-linked-worktree.sh.
// The launcher is invoked as a real host process and host Docker is accessed
// through Moby; focused embedded workloads exercise the container itself.
func TestSysboxLinkedWorktreeGo(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox smoke test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	fixture.startHostSentinel()

	environment := fixture.startEnvironmentProbe()
	fixture.waitForFile(fixture.environmentReady, environment)
	fixture.assertEnvironment()

	worktree := fixture.startWorktreeProbe()
	worktree.requireExit(t, "worktree probe")
	fixture.assertWorktree()

	nestedDocker := fixture.startNestedDockerProbe()
	fixture.waitForFile(fixture.nestedReady, nestedDocker)
	fixture.assertNestedDocker()
	fixture.assertOuterContainer()

	reused := fixture.startReuseCommand()
	fixture.waitForFile(fixture.reuseReport, reused)
	fixture.assertReuse()

	fixture.release(fixture.environmentRelease, environment, "environment command")
	require.True(t, reused.running(), "reused command must survive environment command exit")
	require.True(t, nestedDocker.running(), "nested Docker command must survive environment command exit")
	require.True(t, fixture.inspectOuter().State.Running, "outer session must remain running after one command exits")

	fixture.release(fixture.nestedRelease, nestedDocker, "nested Docker command")
	require.True(t, reused.running(), "reused command must survive nested Docker command exit")
	fixture.release(fixture.releaseSecond, reused, "reused command")
	require.True(t, fixture.inspectOuter().State.Running, "idle grace period must retain the outer session")
	fixture.waitForOuterRemoval()
	fixture.assertAfterNestedDockerCleanup()

	fixture.assertConcurrentCreation()
}

func (fixture *smokeFixture) assertEnvironment() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.environmentReport)
	require.Equal(fixture.t, fixture.hostUser, report["user"], "container user name must match the host")
	require.Equal(fixture.t, fixture.hostGroup, report["group"], "container primary group must match the host")
	require.Equal(fixture.t, fixture.hostHome, report["home"], "container home must preserve the host path")
	require.Equal(fixture.t, fixture.hostHome, report["passwd_home"], "passwd home must preserve the host path")
	require.Equal(fixture.t, "0", report["sudo_uid"], "host user must receive passwordless container-root access")
	require.Equal(fixture.t, "440", report["sudoers_mode"], "sudoers policy must have mode 0440")
	require.Equal(fixture.t, "false", report["sudoers_writable"], "host user must not modify its sudoers policy")
	require.Equal(fixture.t, fixture.gitMarker, report["git_marker"], "mounted global Git config must be visible")
	require.Equal(fixture.t, "false", report["git_writable"], "mounted global Git config must be read-only")
	require.Equal(fixture.t, "UTF-8", report["locale"], "container locale must support UTF-8")
	colors, err := strconv.Atoi(report["colors"])
	require.NoError(fixture.t, err, "terminal color count must be numeric")
	require.GreaterOrEqual(fixture.t, colors, 256, "terminal must advertise at least 256 colors")
	require.Equal(fixture.t, "true", report["color_prompt"], "interactive Bash prompt must use colors")
	require.Equal(fixture.t, "true", report["color_ls"], "interactive Bash must configure color-aware ls")
	require.Equal(fixture.t, "true", report["tools"], "less, make, rg, Docker Compose, and Make completion must be available")
}

func (fixture *smokeFixture) assertWorktree() {
	fixture.t.Helper()
	require.Equal(fixture.t, "Привет из codex-safe\n", readFile(fixture.t, fixture.cyrillic), "Cyrillic project data must round-trip")
	require.Equal(fixture.t, "staged by Sysbox probe\n", readFile(fixture.t, fixture.staged), "probe must stage a linked-worktree file")
	require.Equal(fixture.t, "primary baseline\n", readFile(fixture.t, filepath.Join(fixture.primary, "baseline.txt")), "primary checkout must remain read-only")
	requireHostOwnership(fixture.t, fixture.project, fixture.primary)
}

func (fixture *smokeFixture) assertNestedDocker() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.nestedReport)
	require.NotEmpty(fixture.t, report["nested_daemon"], "nested Docker daemon ID must be present")
	require.NotEqual(fixture.t, fixture.hostDaemonID, report["nested_daemon"], "nested Docker daemon must differ from host Docker")
	require.Equal(fixture.t, "crun", report["nested_runtime"], "nested Docker default runtime must be crun")
	require.Equal(fixture.t, "false", report["sentinel_visible"], "nested Docker must not see a host container")
	require.Equal(fixture.t, "true", report["nested_running"], "nested Docker container must be running")
	require.Equal(fixture.t, "true", report["compose_running"], "nested Compose service must be running")
	require.Equal(fixture.t, "nested marker\n", readFile(fixture.t, fixture.nestedMarker), "nested Docker bind mount must write the project")
}

func (fixture *smokeFixture) assertOuterContainer() {
	fixture.t.Helper()
	inspection := fixture.inspectOuter()
	require.True(fixture.t, inspection.State.Running, "outer container must run while probe command is active")
	require.Equal(fixture.t, "true", inspection.Config.Labels["codex-safe.managed"], "managed label must identify the session")
	require.Equal(fixture.t, fixture.project, inspection.Config.Labels["codex-safe.project-path"], "project label must name the linked worktree")
	require.Equal(fixture.t, strconv.Itoa(os.Getuid()), inspection.Config.Labels["codex-safe.host-uid"], "host UID label must be present")
	require.Equal(fixture.t, "1", inspection.Config.Labels["codex-safe.manager-protocol"], "manager protocol label must be present")
	require.Equal(fixture.t, fixture.nested, inspection.Config.WorkingDir, "outer working directory must preserve nested invocation path")
	require.Equal(fixture.t, "sysbox-runc", inspection.HostConfig.Runtime, "outer container must use Sysbox runtime")
	require.False(fixture.t, inspection.HostConfig.Privileged, "outer container must not be privileged")
	requireMount(fixture.t, inspection, fixture.primary, fixture.primary, false)
	requireMount(fixture.t, inspection, filepath.Join(fixture.primary, ".git"), filepath.Join(fixture.primary, ".git"), true)
	requireMount(fixture.t, inspection, fixture.project, fixture.project, true)
	requireMount(fixture.t, inspection, fixture.hostGit, fixture.hostGit, false)
	for _, mount := range inspection.Mounts {
		require.NotEqual(fixture.t, "/var/run/docker.sock", mount.Source, "host Docker socket must not be mounted")
		require.NotEqual(fixture.t, "/var/run/docker.sock", mount.Destination, "container Docker socket must not be mounted")
	}
	require.Len(fixture.t, fixture.managedContainers(), 1, "there must be exactly one active managed outer container")
}

func (fixture *smokeFixture) assertReuse() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.reuseReport)
	inspection := fixture.inspectOuter()
	require.Equal(fixture.t, inspection.Config.Hostname, report["hostname"], "reused command must run in the same outer container")
	require.Equal(fixture.t, fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), report["identity"], "reused command must retain host identity")
	require.Equal(fixture.t, fixture.project, report["pwd"], "reused command must preserve its requested working directory")
	require.NotEmpty(fixture.t, report["nested_daemon"], "reused command must access nested Docker")
	require.Equal(fixture.t, parseReport(fixture.t, fixture.nestedReport)["nested_daemon"], report["nested_daemon"], "reused command must retain nested Docker daemon")
	require.Len(fixture.t, fixture.managedContainers(), 1, "reuse must not create a second outer container")
}

func (fixture *smokeFixture) assertAfterNestedDockerCleanup() {
	fixture.t.Helper()
	require.Empty(fixture.t, fixture.containersNamed(fixture.nestedName), "nested container must never appear in host Docker")
	require.Empty(fixture.t, fixture.containersNamed(fixture.composeName), "Compose container must never appear in host Docker")
	sentinel, err := fixture.docker.ContainerInspect(fixture.ctx, fixture.sentinel)
	require.NoError(fixture.t, err, "host sentinel must remain inspectable")
	require.True(fixture.t, sentinel.State.Running, "host sentinel must remain running after nested cleanup")
	staged := runOutput(fixture.t, fixture.project, "git", "diff", "--cached", "--name-only")
	require.Contains(fixture.t, staged, filepath.Base(fixture.staged), "linked-worktree file must remain staged")
	requireHostOwnership(fixture.t, fixture.project, filepath.Join(fixture.primary, ".git"))
}

func (fixture *smokeFixture) assertConcurrentCreation() {
	fixture.t.Helper()
	marker := filepath.Join(fixture.project, "concurrent.release")
	first := fixture.start(fixture.project, "bash", "-c", waitScript, "bash", marker)
	second := fixture.start(fixture.project, "bash", "-c", waitScript, "bash", marker)
	fixture.waitForOuter()
	require.Len(fixture.t, fixture.managedContainers(), 1, "concurrent first callers must create only one outer container")
	fixture.release(marker, first, "first concurrent caller")
	second.requireExit(fixture.t, "second concurrent caller")
	fixture.waitForOuterRemoval()
}
