package smoke_test

import (
	"bytes"
	"context"
	_ "embed"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

const (
	goSmokeEnv     = "CODEX_SAFE_RUN_SYSBOX_SMOKE"
	goSmokeImage   = "codex-safe-mvp:local"
	nestedImage    = "alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"
	smokeTimeout   = 3 * time.Minute
	commandTimeout = 90 * time.Second
)

type smokeFixture struct {
	t                  *testing.T
	ctx                context.Context
	cancel             context.CancelFunc
	docker             *client.Client
	binary             string
	root               string
	primary            string
	project            string
	nested             string
	hostHome           string
	hostGit            string
	hostUser           string
	hostGroup          string
	hostDaemonID       string
	container          string
	sentinel           string
	nestedName         string
	composeName        string
	composeFile        string
	composeProj        string
	gitMarker          string
	environmentReport  string
	environmentReady   string
	environmentRelease string
	nestedReport       string
	nestedReady        string
	nestedRelease      string
	reuseReport        string
	releaseSecond      string
	nestedMarker       string
	staged             string
	cyrillic           string
}

func newSmokeFixture(t *testing.T) *smokeFixture {
	t.Helper()
	workingDirectory, err := os.Getwd()
	require.NoError(t, err, "smoke working directory must be available")
	binary := filepath.Join(workingDirectory, "..", "..", "bin", "codex-safe")
	if _, err := os.Stat(binary); err != nil {
		t.Skip("bin/codex-safe is missing; run make build first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "Moby client must initialize from the Docker environment")
	info, err := dockerClient.Info(ctx)
	require.NoError(t, err, "Moby client must inspect the host Docker daemon")

	root := t.TempDir()
	primary := filepath.Join(root, "primary repo")
	project := filepath.Join(root, "feature worktree")
	nested := filepath.Join(project, "nested directory")
	initGitProject(t, primary)
	runInDir(t, primary, "git", "worktree", "add", "-b", "smoke/feature", project)
	require.NoError(t, os.MkdirAll(nested, 0o755), "nested project directory must be created")

	hostHome := filepath.Join(root, "host home")
	require.NoError(t, os.MkdirAll(hostHome, 0o755), "temporary host home must be created")
	gitMarker := "go-smoke-marker"
	hostGit := filepath.Join(hostHome, ".gitconfig")
	require.NoError(t, os.WriteFile(hostGit, []byte("[codex-safe-smoke]\n\tmarker = "+gitMarker+"\n"), 0o400), "temporary host Git config must be written")

	currentUser, err := user.Current()
	require.NoError(t, err, "host user must be resolvable")
	group, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	require.NoError(t, err, "host primary group must be resolvable")
	containerName, err := launcher.ProjectContainerName(os.Getuid(), project)
	require.NoError(t, err, "deterministic project container name must be derivable")

	fixture := &smokeFixture{
		t:                  t,
		ctx:                ctx,
		cancel:             cancel,
		docker:             dockerClient,
		binary:             binary,
		root:               root,
		primary:            primary,
		project:            project,
		nested:             nested,
		hostHome:           hostHome,
		hostGit:            hostGit,
		hostUser:           currentUser.Username,
		hostGroup:          group.Name,
		hostDaemonID:       info.ID,
		container:          containerName,
		sentinel:           "codex-safe-host-sentinel-" + filepath.Base(root),
		nestedName:         "codex-safe-nested-" + filepath.Base(root),
		composeName:        "codex-safe-compose-" + filepath.Base(root),
		composeFile:        filepath.Join(project, ".codex-safe-compose.yaml"),
		composeProj:        "codex-safe-" + filepath.Base(root),
		gitMarker:          gitMarker,
		environmentReport:  filepath.Join(project, "environment.report"),
		environmentReady:   filepath.Join(project, "environment.ready"),
		environmentRelease: filepath.Join(project, "environment.release"),
		nestedReport:       filepath.Join(project, "nested-docker.report"),
		nestedReady:        filepath.Join(project, "nested-docker.ready"),
		nestedRelease:      filepath.Join(project, "nested-docker.release"),
		reuseReport:        filepath.Join(project, "reuse.report"),
		releaseSecond:      filepath.Join(project, "reuse.release"),
		nestedMarker:       filepath.Join(project, "nested.marker"),
		staged:             filepath.Join(project, "staged-by-probe.txt"),
		cyrillic:           filepath.Join(project, "cyrillic.txt"),
	}
	t.Cleanup(fixture.cleanup)
	return fixture
}

func (fixture *smokeFixture) cleanup() {
	fixture.cancel()
	cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = fixture.docker.ContainerRemove(cleanupContext, fixture.container, container.RemoveOptions{Force: true})
	_ = fixture.docker.ContainerRemove(cleanupContext, fixture.sentinel, container.RemoveOptions{Force: true})
	_ = fixture.docker.Close()
}

func (fixture *smokeFixture) startHostSentinel() {
	fixture.t.Helper()
	created, err := fixture.docker.ContainerCreate(fixture.ctx, &container.Config{
		Image:      goSmokeImage,
		Entrypoint: []string{"/bin/sleep"},
		Cmd:        []string{"300"},
		Labels:     map[string]string{"codex-safe.smoke": "go"},
	}, nil, nil, nil, fixture.sentinel)
	require.NoError(fixture.t, err, "host sentinel container must be created")
	require.NoError(fixture.t, fixture.docker.ContainerStart(fixture.ctx, created.ID, container.StartOptions{}), "host sentinel container must start")
}

func (fixture *smokeFixture) startEnvironmentProbe() *launcherProcess {
	fixture.t.Helper()
	return fixture.start(fixture.nested, "bash", "-c", environmentProbeScript, "bash",
		fixture.environmentReport, fixture.environmentReady, fixture.environmentRelease,
		fixture.hostUser, fixture.hostGroup, fixture.gitMarker, fixture.hostHome)
}

func (fixture *smokeFixture) startWorktreeProbe() *launcherProcess {
	fixture.t.Helper()
	return fixture.start(fixture.project, "bash", "-c", worktreeProbeScript, "bash",
		fixture.project, fixture.primary, fixture.staged, fixture.cyrillic)
}

func (fixture *smokeFixture) startNestedDockerProbe() *launcherProcess {
	fixture.t.Helper()
	return fixture.start(fixture.project, "bash", "-c", nestedDockerProbeScript, "bash",
		fixture.nestedReport, fixture.nestedReady, fixture.nestedRelease, fixture.project,
		fixture.nestedMarker, fixture.sentinel, fixture.nestedName, fixture.composeFile,
		fixture.composeProj, fixture.composeName, nestedImage)
}

func (fixture *smokeFixture) startReuseCommand() *launcherProcess {
	fixture.t.Helper()
	return fixture.start(fixture.project, "bash", "-c", reuseScript, "bash", fixture.reuseReport, fixture.releaseSecond)
}

func (fixture *smokeFixture) start(project string, command ...string) *launcherProcess {
	fixture.t.Helper()
	arguments := append([]string{"--project", project, "--image", goSmokeImage, "--"}, command...)
	process := exec.Command(fixture.binary, arguments...)
	process.Env = append(os.Environ(), "HOME="+fixture.hostHome)
	launcher := &launcherProcess{command: process, done: make(chan struct{})}
	process.Stdout = &launcher.stdout
	process.Stderr = &launcher.stderr
	require.NoError(fixture.t, process.Start(), "codex-safe command must start: %s", strings.Join(arguments, " "))
	go func() {
		launcher.err = process.Wait()
		close(launcher.done)
	}()
	return launcher
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

func (fixture *smokeFixture) inspectOuter() container.InspectResponse {
	fixture.t.Helper()
	inspection, err := fixture.docker.ContainerInspect(fixture.ctx, fixture.container)
	require.NoError(fixture.t, err, "Moby client must inspect deterministic container %q", fixture.container)
	return inspection
}

func (fixture *smokeFixture) managedContainers() []container.Summary {
	fixture.t.Helper()
	items, err := fixture.docker.ContainerList(fixture.ctx, container.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", "codex-safe.managed=true"),
		filters.Arg("label", "codex-safe.project-path="+fixture.project),
		filters.Arg("label", "codex-safe.host-uid="+strconv.Itoa(os.Getuid())),
		filters.Arg("name", "^"+fixture.container+"$"),
	)})
	require.NoError(fixture.t, err, "Moby client must list active managed containers")
	return items
}

func (fixture *smokeFixture) containersNamed(name string) []container.Summary {
	fixture.t.Helper()
	items, err := fixture.docker.ContainerList(fixture.ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", "^"+name+"$")),
	})
	require.NoError(fixture.t, err, "Moby client must list containers named %q", name)
	return items
}

func (fixture *smokeFixture) waitForOuter() {
	fixture.t.Helper()
	deadline := time.Now().Add(commandTimeout)
	for time.Now().Before(deadline) {
		if len(fixture.managedContainers()) == 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fixture.t.Fatal("timed out waiting for the deterministic outer container")
}

func (fixture *smokeFixture) waitForOuterRemoval() {
	fixture.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, err := fixture.docker.ContainerInspect(fixture.ctx, fixture.container)
		if client.IsErrNotFound(err) {
			return
		}
		require.NoError(fixture.t, err, "Moby client must inspect the session while waiting for idle removal")
		time.Sleep(250 * time.Millisecond)
	}
	fixture.t.Fatal("deterministic outer container was not removed after idle timeout")
}

type launcherProcess struct {
	command *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	done    chan struct{}
	err     error
}

func (process *launcherProcess) running() bool {
	select {
	case <-process.done:
		return false
	default:
		return true
	}
}

func (process *launcherProcess) requireExit(t *testing.T, description string) {
	t.Helper()
	select {
	case <-process.done:
		require.NoError(t, process.err, "%s must exit cleanly\n%s", description, process.diagnostics())
	case <-time.After(commandTimeout):
		t.Fatalf("timed out waiting for %s\n%s", description, process.diagnostics())
	}
}

func (process *launcherProcess) diagnostics() string {
	return "stdout:\n" + process.stdout.String() + "\nstderr:\n" + process.stderr.String()
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
