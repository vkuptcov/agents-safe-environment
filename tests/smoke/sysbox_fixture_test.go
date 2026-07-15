package smoke_test

import (
	_ "embed"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	goSmokeEnv     = "CODEX_SAFE_RUN_SYSBOX_SMOKE"
	goSmokeImage   = "codex-safe-mvp:local"
	nestedImage    = "alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"
	smokeTimeout   = 3 * time.Minute
	commandTimeout = 90 * time.Second
)

// smokeFixture only composes the independent pieces of the smoke harness.
// Setup, process execution, and Docker access live in their own focused types.
type smokeFixture struct {
	t        *testing.T
	project  projectLayout
	host     hostIdentity
	files    smokeArtifacts
	launcher *launcherHarness
	docker   *dockerHarness
}

func newSmokeFixture(t *testing.T) *smokeFixture {
	t.Helper()
	project := newProjectLayout(t)
	host := newHostIdentity(t, project)
	fixture := &smokeFixture{
		t:        t,
		project:  project,
		host:     host,
		files:    newSmokeArtifacts(project),
		launcher: newLauncherHarness(t, project.hostHome),
		docker:   newDockerHarness(t, project),
	}
	t.Cleanup(fixture.docker.close)
	return fixture
}

func (fixture *smokeFixture) startEnvironmentProbe() *launcherProcess {
	fixture.t.Helper()
	probe := fixture.files.environment
	return fixture.launcher.start(fixture.project.nested, "bash", "-c", environmentProbeScript, "bash",
		probe.report, probe.ready, probe.release)
}

func (fixture *smokeFixture) startWorktreeProbe() *launcherProcess {
	fixture.t.Helper()
	return fixture.launcher.start(fixture.project.worktree, "bash", "-c", worktreeProbeScript, "bash",
		fixture.project.worktree, fixture.project.primary, fixture.files.staged, fixture.files.cyrillic)
}

func (fixture *smokeFixture) startNestedDockerProbe() *launcherProcess {
	fixture.t.Helper()
	probe := fixture.files.nestedDocker
	environment := []string{
		"REPORT=" + probe.report,
		"READY=" + probe.ready,
		"RELEASE=" + probe.release,
		"LINKED_WORKTREE=" + fixture.project.worktree,
		"NESTED_MARKER=" + fixture.files.nestedMarker,
		"HOST_SENTINEL_NAME=" + fixture.docker.names.sentinel,
		"NESTED_CONTAINER_NAME=" + fixture.docker.names.nested,
		"COMPOSE_FILE=" + fixture.files.composeFile,
		"COMPOSE_PROJECT=" + fixture.files.composeProject,
		"COMPOSE_CONTAINER_NAME=" + fixture.docker.names.compose,
		"NESTED_IMAGE=" + nestedImage,
	}
	return fixture.launcher.startWithEnvironment(
		fixture.project.worktree,
		environment,
		"bash", "-c", nestedDockerProbeScript,
	)
}

func (fixture *smokeFixture) startReuseCommand() *launcherProcess {
	fixture.t.Helper()
	probe := fixture.files.reuse
	return fixture.launcher.start(fixture.project.worktree, "bash", "-c", reuseScript, "bash", probe.report, probe.release)
}

func (fixture *smokeFixture) release(path string, process *launcherProcess, description string) {
	fixture.t.Helper()
	require.NoError(fixture.t, os.WriteFile(path, nil, 0o600), "%s release marker must be writable", description)
	process.requireExit(fixture.t, description)
}

func (fixture *smokeFixture) waitForFile(path string, process *launcherProcess) {
	fixture.t.Helper()
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-process.done:
			fixture.t.Fatalf("command exited before creating %q: %v\n%s", path, process.err, process.diagnostics())
		case <-timer.C:
			fixture.t.Fatalf("timed out waiting for %q\n%s", path, process.diagnostics())
		case <-ticker.C:
		}
	}
}

const reuseScript = `report=$1; release=$2
printf 'hostname=%s\nidentity=%s:%s\npwd=%s\nnested_daemon=%s\n' "$(hostname)" "$(id -u)" "$(id -g)" "$PWD" "$(docker info --format '{{.ID}}')" > "$report"
while [[ ! -e "$release" ]]; do sleep 1; done`

const waitScript = `while [[ ! -e "$1" ]]; do sleep 1; done`

//go:embed testdata/environment-probe.sh
var environmentProbeScript string

//go:embed testdata/worktree-probe.sh
var worktreeProbeScript string

//go:embed testdata/nested-docker-probe.sh
var nestedDockerProbeScript string
