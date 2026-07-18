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
	root      string
	primary   string
	worktree  string
	nested    string
	hostHome  string
	hostGit   string
	codexHome string
}

func newProjectLayout(t *testing.T, createCodexHome bool) projectLayout {
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
	layout.codexHome = filepath.Join(layout.hostHome, ".codex")

	initGitProject(t, layout.primary)
	runInDir(t, layout.primary, "git", "worktree", "add", "-b", "smoke/feature", layout.worktree)
	require.NoError(t, os.MkdirAll(layout.nested, 0o755), "nested project directory must be created")
	require.NoError(t, os.MkdirAll(layout.hostHome, 0o755), "temporary host home must be created")
	if createCodexHome {
		require.NoError(t, os.MkdirAll(layout.codexHome, 0o755), "temporary Codex home must be created")
	}
	return layout
}

type hostIdentity struct {
	user      string
	group     string
	gitMarker string
	gitConfig string
}

func newHostIdentity(t *testing.T, project projectLayout) hostIdentity {
	t.Helper()
	identity := hostIdentity{gitMarker: "go-smoke-marker"}
	identity.gitConfig = "[codex-safe-smoke]\n\tmarker = " + identity.gitMarker + "\n"
	require.NoError(t, os.WriteFile(
		project.hostGit,
		[]byte(identity.gitConfig),
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
	t             *testing.T
	productBinary string
	agentsBinary  string
	hostHome      string
}

func newLauncherHarness(t *testing.T, hostHome string) *launcherHarness {
	t.Helper()
	workingDirectory, err := os.Getwd()
	require.NoError(t, err, "smoke working directory must be available")
	agents := filepath.Join(workingDirectory, "..", "..", "bin", "agents-safe")
	if _, err := os.Stat(agents); err != nil {
		t.Skip("bin/agents-safe is missing; run make build first")
	}
	product := filepath.Join(workingDirectory, "..", "..", "bin", "codex-safe")
	return &launcherHarness{t: t, productBinary: product, agentsBinary: agents, hostHome: hostHome}
}

// launcherEnv builds the launcher process environment. It removes any ambient HOME and CODEX_HOME
// so Codex-home resolution is deterministic, sets HOME to the synthetic host home, then applies the
// caller's overrides (for example an explicit CODEX_HOME for the reuse-mismatch scenario).
func (launcher *launcherHarness) launcherEnv(extra []string) []string {
	environment := make([]string, 0, len(os.Environ())+1+len(extra))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "CODEX_HOME=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "HOME="+launcher.hostHome)
	return append(environment, extra...)
}

func (launcher *launcherHarness) startBinary(
	binary string,
	project string,
	separator bool,
	hostEnv []string,
	command ...string,
) *launcherProcess {
	return launcher.startBinaryWithImageOverride(binary, project, true, separator, hostEnv, command...)
}

func (launcher *launcherHarness) startBinaryWithImageOverride(
	binary string,
	project string,
	imageOverride bool,
	separator bool,
	hostEnv []string,
	command ...string,
) *launcherProcess {
	launcher.t.Helper()
	arguments := []string{"--project", project}
	if imageOverride {
		arguments = append(arguments, "--image", goSmokeImage)
	}
	if separator {
		arguments = append(arguments, "--")
	}
	arguments = append(arguments, command...)
	process := exec.Command(binary, arguments...)
	process.Env = launcher.launcherEnv(hostEnv)
	running := &launcherProcess{command: process, done: make(chan struct{})}
	process.Stdout = &running.stdout
	process.Stderr = &running.stderr
	require.NoError(launcher.t, process.Start(), "%s command must start: %s", filepath.Base(binary), strings.Join(arguments, " "))
	go func() {
		running.err = process.Wait()
		close(running.done)
	}()
	return running
}

func (launcher *launcherHarness) start(project string, command ...string) *launcherProcess {
	launcher.t.Helper()
	return launcher.startBinary(launcher.agentsBinary, project, true, nil, command...)
}

// startDefault invokes the public launcher without --image, so normal project-environment discovery applies.
func (launcher *launcherHarness) startDefault(project string, command ...string) *launcherProcess {
	launcher.t.Helper()
	return launcher.startBinaryWithImageOverride(launcher.agentsBinary, project, false, true, nil, command...)
}

// startAgents invokes the public generic launcher without a separator, exercising the documented
// `agents-safe bash` argument form.
func (launcher *launcherHarness) startAgents(project string, command ...string) *launcherProcess {
	launcher.t.Helper()
	return launcher.startBinary(launcher.agentsBinary, project, false, nil, command...)
}

func (launcher *launcherHarness) startWithEnvironment(project string, environment []string, command ...string) *launcherProcess {
	launcher.t.Helper()
	containerCommand := append([]string{"env"}, environment...)
	containerCommand = append(containerCommand, command...)
	return launcher.start(project, containerCommand...)
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
