package smoke_test

import (
	"bytes"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type projectLayout struct {
	root     string
	primary  string
	worktree string
	nested   string
	hostHome string
	hostGit  string
}

func newProjectLayout(t *testing.T) projectLayout {
	t.Helper()
	root := t.TempDir()
	layout := projectLayout{
		root:     root,
		primary:  filepath.Join(root, "primary repo"),
		worktree: filepath.Join(root, "feature worktree"),
		hostHome: filepath.Join(root, "host home"),
	}
	layout.nested = filepath.Join(layout.worktree, "nested directory")
	layout.hostGit = filepath.Join(layout.hostHome, ".gitconfig")

	initGitProject(t, layout.primary)
	runInDir(t, layout.primary, "git", "worktree", "add", "-b", "smoke/feature", layout.worktree)
	require.NoError(t, os.MkdirAll(layout.nested, 0o755), "nested project directory must be created")
	require.NoError(t, os.MkdirAll(layout.hostHome, 0o755), "temporary host home must be created")
	return layout
}

type hostIdentity struct {
	user      string
	group     string
	gitMarker string
}

func newHostIdentity(t *testing.T, project projectLayout) hostIdentity {
	t.Helper()
	identity := hostIdentity{gitMarker: "go-smoke-marker"}
	require.NoError(t, os.WriteFile(
		project.hostGit,
		[]byte("[codex-safe-smoke]\n\tmarker = "+identity.gitMarker+"\n"),
		0o400,
	), "temporary host Git config must be written")
	currentUser, err := user.Current()
	require.NoError(t, err, "host user must be resolvable")
	group, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	require.NoError(t, err, "host primary group must be resolvable")
	identity.user = currentUser.Username
	identity.group = group.Name
	return identity
}

type probeArtifacts struct {
	report  string
	ready   string
	release string
}

type smokeArtifacts struct {
	environment    probeArtifacts
	nestedDocker   probeArtifacts
	reuse          probeArtifacts
	nestedMarker   string
	staged         string
	cyrillic       string
	composeFile    string
	composeProject string
}

func newSmokeArtifacts(project projectLayout) smokeArtifacts {
	probe := func(name string) probeArtifacts {
		return probeArtifacts{
			report:  filepath.Join(project.worktree, name+".report"),
			ready:   filepath.Join(project.worktree, name+".ready"),
			release: filepath.Join(project.worktree, name+".release"),
		}
	}
	return smokeArtifacts{
		environment:    probe("environment"),
		nestedDocker:   probe("nested-docker"),
		reuse:          probe("reuse"),
		nestedMarker:   filepath.Join(project.worktree, "nested.marker"),
		staged:         filepath.Join(project.worktree, "staged-by-probe.txt"),
		cyrillic:       filepath.Join(project.worktree, "cyrillic.txt"),
		composeFile:    filepath.Join(project.worktree, ".codex-safe-compose.yaml"),
		composeProject: "codex-safe-" + filepath.Base(project.root),
	}
}

type launcherHarness struct {
	t        *testing.T
	binary   string
	hostHome string
}

func newLauncherHarness(t *testing.T, hostHome string) *launcherHarness {
	t.Helper()
	workingDirectory, err := os.Getwd()
	require.NoError(t, err, "smoke working directory must be available")
	binary := filepath.Join(workingDirectory, "..", "..", "bin", "codex-safe")
	if _, err := os.Stat(binary); err != nil {
		t.Skip("bin/codex-safe is missing; run make build first")
	}
	return &launcherHarness{t: t, binary: binary, hostHome: hostHome}
}

func (launcher *launcherHarness) start(project string, command ...string) *launcherProcess {
	launcher.t.Helper()
	arguments := append([]string{"--project", project, "--image", goSmokeImage, "--"}, command...)
	process := exec.Command(launcher.binary, arguments...)
	process.Env = append(os.Environ(), "HOME="+launcher.hostHome)
	running := &launcherProcess{command: process, done: make(chan struct{})}
	process.Stdout = &running.stdout
	process.Stderr = &running.stderr
	require.NoError(launcher.t, process.Start(), "codex-safe command must start: %s", strings.Join(arguments, " "))
	go func() {
		running.err = process.Wait()
		close(running.done)
	}()
	return running
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
