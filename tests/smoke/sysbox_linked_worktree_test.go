package smoke_test

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// TestSysboxLinkedWorktreeGo is the Go equivalent of sysbox-linked-worktree.sh.
// The launcher is still invoked as a real host CLI process; Moby is used for
// every host-Docker assertion and lifecycle operation.
func TestSysboxLinkedWorktreeGo(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox smoke test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	fixture.startHostSentinel()

	first := fixture.startProbe()
	fixture.waitForFile(fixture.ready, first)
	fixture.assertProbeResult()
	fixture.assertOuterContainer()

	second := fixture.startReuseCommand()
	fixture.waitForFile(fixture.reuseReport, second)
	fixture.assertReuse()

	fixture.release(fixture.releaseFirst, first, "first command")
	require.True(t, second.running(), "second wrapped command must survive first command exit")
	require.True(t, fixture.inspectOuter().State.Running, "outer session must remain running after first command exit")

	fixture.release(fixture.releaseSecond, second, "second command")
	require.True(t, fixture.inspectOuter().State.Running, "idle grace period must retain the outer session")
	fixture.waitForOuterRemoval()
	fixture.assertAfterProbeCleanup()

	fixture.assertConcurrentCreation()
}

type smokeFixture struct {
	t             *testing.T
	ctx           context.Context
	cancel        context.CancelFunc
	docker        *client.Client
	binary        string
	root          string
	primary       string
	project       string
	nested        string
	hostHome      string
	hostGit       string
	hostUser      string
	hostGroup     string
	hostDaemonID  string
	container     string
	sentinel      string
	nestedName    string
	composeName   string
	composeFile   string
	composeProj   string
	gitMarker     string
	report        string
	ready         string
	releaseFirst  string
	reuseReport   string
	releaseSecond string
	nestedMarker  string
	staged        string
	cyrillic      string
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
		t:             t,
		ctx:           ctx,
		cancel:        cancel,
		docker:        dockerClient,
		binary:        binary,
		root:          root,
		primary:       primary,
		project:       project,
		nested:        nested,
		hostHome:      hostHome,
		hostGit:       hostGit,
		hostUser:      currentUser.Username,
		hostGroup:     group.Name,
		hostDaemonID:  info.ID,
		container:     containerName,
		sentinel:      "codex-safe-host-sentinel-" + filepath.Base(root),
		nestedName:    "codex-safe-nested-" + filepath.Base(root),
		composeName:   "codex-safe-compose-" + filepath.Base(root),
		composeFile:   filepath.Join(project, ".codex-safe-compose.yaml"),
		composeProj:   "codex-safe-" + filepath.Base(root),
		gitMarker:     gitMarker,
		report:        filepath.Join(project, "probe.report"),
		ready:         filepath.Join(project, "probe.ready"),
		releaseFirst:  filepath.Join(project, "probe.release"),
		reuseReport:   filepath.Join(project, "reuse.report"),
		releaseSecond: filepath.Join(project, "reuse.release"),
		nestedMarker:  filepath.Join(project, "nested.marker"),
		staged:        filepath.Join(project, "staged-by-probe.txt"),
		cyrillic:      filepath.Join(project, "cyrillic.txt"),
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

func (fixture *smokeFixture) startProbe() *launcherProcess {
	fixture.t.Helper()
	return fixture.start(fixture.nested, "bash", "-c", probeScript, "bash",
		fixture.report, fixture.ready, fixture.releaseFirst, fixture.project, fixture.primary,
		fixture.nestedMarker, fixture.hostUser, fixture.hostGroup, fixture.gitMarker, fixture.hostHome,
		fixture.sentinel, fixture.nestedName, fixture.composeFile, fixture.composeProj, fixture.composeName,
		fixture.staged, fixture.cyrillic, nestedImage)
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

func (fixture *smokeFixture) assertProbeResult() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.report)
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
	require.NotEmpty(fixture.t, report["nested_daemon"], "nested Docker daemon ID must be present")
	require.NotEqual(fixture.t, fixture.hostDaemonID, report["nested_daemon"], "nested Docker daemon must differ from host Docker")
	require.Equal(fixture.t, "crun", report["nested_runtime"], "nested Docker default runtime must be crun")
	require.Equal(fixture.t, "false", report["sentinel_visible"], "nested Docker must not see a host container")
	require.Equal(fixture.t, "true", report["nested_running"], "nested Docker container must be running")
	require.Equal(fixture.t, "true", report["compose_running"], "nested Compose service must be running")

	require.Equal(fixture.t, "nested marker\n", readFile(fixture.t, fixture.nestedMarker), "nested Docker bind mount must write the project")
	require.Equal(fixture.t, "Привет из codex-safe\n", readFile(fixture.t, fixture.cyrillic), "Cyrillic project data must round-trip")
	require.Equal(fixture.t, "staged by Sysbox probe\n", readFile(fixture.t, fixture.staged), "probe must stage a linked-worktree file")
	require.Equal(fixture.t, "primary baseline\n", readFile(fixture.t, filepath.Join(fixture.primary, "baseline.txt")), "primary checkout must remain read-only")
	requireHostOwnership(fixture.t, fixture.project, fixture.primary)
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
	managed := fixture.managedContainers()
	require.Len(fixture.t, managed, 1, "there must be exactly one active managed outer container")
}

func (fixture *smokeFixture) assertReuse() {
	fixture.t.Helper()
	report := parseReport(fixture.t, fixture.reuseReport)
	inspection := fixture.inspectOuter()
	require.Equal(fixture.t, inspection.Config.Hostname, report["hostname"], "reused command must run in the same outer container")
	require.Equal(fixture.t, fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), report["identity"], "reused command must retain host identity")
	require.Equal(fixture.t, fixture.project, report["pwd"], "reused command must preserve its requested working directory")
	require.NotEmpty(fixture.t, report["nested_daemon"], "reused command must access nested Docker")
	require.Equal(fixture.t, parseReport(fixture.t, fixture.report)["nested_daemon"], report["nested_daemon"], "reused command must retain nested Docker daemon")
	require.Len(fixture.t, fixture.managedContainers(), 1, "reuse must not create a second outer container")
}

func (fixture *smokeFixture) assertAfterProbeCleanup() {
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
	items, err := fixture.docker.ContainerList(fixture.ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("name", "^"+name+"$"))})
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

func initGitProject(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755), "project directory must be created")
	runInDir(t, path, "git", "init", "-q")
	runInDir(t, path, "git", "config", "user.name", "Codex Safe Smoke")
	runInDir(t, path, "git", "config", "user.email", "codex-safe@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(path, "baseline.txt"), []byte("primary baseline\n"), 0o600), "baseline must be written")
	runInDir(t, path, "git", "add", "baseline.txt")
	runInDir(t, path, "git", "commit", "-qm", "baseline")
}

func requireMount(t *testing.T, inspection container.InspectResponse, source, destination string, writable bool) {
	t.Helper()
	for _, mount := range inspection.Mounts {
		if mount.Source == source && mount.Destination == destination {
			require.Equal(t, writable, mount.RW, "mount %q -> %q must have the expected read-write mode", source, destination)
			require.Equal(t, "rprivate", string(mount.Propagation), "mount %q -> %q must use private propagation", source, destination)
			return
		}
	}
	t.Fatalf("required mount %q -> %q was not found", source, destination)
}

func parseReport(t *testing.T, path string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		key, value, found := strings.Cut(line, "=")
		require.True(t, found, "report line %q must have key=value form", line)
		values[key] = value
	}
	return values
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "file %q must be readable", path)
	return string(data)
}

func requireHostOwnership(t *testing.T, paths ...string) {
	t.Helper()
	for _, root := range paths {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("read ownership of %s", path)
			}
			if int(stat.Uid) != os.Getuid() || int(stat.Gid) != os.Getgid() {
				return fmt.Errorf("%s has owner %d:%d, expected %d:%d", path, stat.Uid, stat.Gid, os.Getuid(), os.Getgid())
			}
			return nil
		})
		require.NoError(t, err, "Sysbox writes under %q must remain owned by the host user", root)
	}
}

func runInDir(t *testing.T, directory, name string, args ...string) {
	t.Helper()
	output := runOutput(t, directory, name, args...)
	_ = output
}

func runOutput(t *testing.T, directory, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %s must succeed: %s", name, strings.Join(args, " "), output)
	return string(output)
}

const reuseScript = `report=$1; release=$2
printf 'hostname=%s\nidentity=%s:%s\npwd=%s\nnested_daemon=%s\n' "$(hostname)" "$(id -u)" "$(id -g)" "$PWD" "$(docker info --format '{{.ID}}')" > "$report"
while [[ ! -e "$release" ]]; do sleep 1; done`

const waitScript = `while [[ ! -e "$1" ]]; do sleep 1; done`

//go:embed testdata/sysbox-probe.sh
var probeScript string
