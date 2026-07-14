package launcher

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildDockerArgsUsesSysboxAndPreservesProbe(t *testing.T) {
	t.Parallel()

	plan := Plan{
		ProjectRoot: "/sources/feature worktree",
		WorkingDir:  "/sources/feature worktree/nested",
		Mounts: []Mount{
			{Source: "/sources/primary", Target: "/sources/primary", ReadOnly: true},
			{Source: "/sources/primary/.git", Target: "/sources/primary/.git"},
			{Source: "/sources/feature worktree", Target: "/sources/feature worktree"},
		},
	}
	probe := []string{"printf", "%s\\n", "value with spaces; $(not-a-shell)"}

	args, err := BuildDockerArgs(
		plan,
		"codex-safe-mvp:local",
		probe,
		"codex-safe-test",
		1000,
		1000,
		"developer",
		"developers",
		"/home/developer",
		"",
		true,
	)
	if err != nil {
		t.Fatalf("BuildDockerArgs() error = %v", err)
	}

	wantPrefix := []string{
		"run",
		"--rm",
		"--interactive",
		"--tty",
		"--runtime=sysbox-runc",
		"--name",
		"codex-safe-test",
		"--label",
		"codex-safe.session=codex-safe-test",
		"--label",
		"codex-safe.project-path=/sources/feature worktree",
		"--label",
		"codex-safe.host-uid=1000",
		"--env",
		"CODEX_SAFE_HOST_UID=1000",
		"--env",
		"CODEX_SAFE_HOST_GID=1000",
		"--env",
		"CODEX_SAFE_HOST_USER=developer",
		"--env",
		"CODEX_SAFE_HOST_GROUP=developers",
		"--env",
		"CODEX_SAFE_HOST_HOME=/home/developer",
		"--workdir",
		plan.WorkingDir,
	}
	if !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) {
		t.Errorf("args prefix = %#v, want %#v", args[:len(wantPrefix)], wantPrefix)
	}
	if !reflect.DeepEqual(args[len(args)-len(probe):], probe) {
		t.Errorf("probe argv = %#v, want %#v", args[len(args)-len(probe):], probe)
	}
	if args[len(args)-len(probe)-1] != "codex-safe-mvp:local" {
		t.Errorf("image = %q, want codex-safe-mvp:local", args[len(args)-len(probe)-1])
	}

	joined := strings.Join(args, " ")
	for _, forbidden := range []string{
		"--privileged",
		"--network=host",
		"--pid=host",
		"/var/run/docker.sock",
	} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("docker args contain forbidden value %q: %s", forbidden, joined)
		}
	}
}

func TestBuildDockerArgsIncludesMountModesInOrder(t *testing.T) {
	t.Parallel()

	args, err := BuildDockerArgs(Plan{
		ProjectRoot: "/worktree",
		WorkingDir:  "/worktree",
		Mounts: []Mount{
			{Source: "/primary", Target: "/primary", ReadOnly: true},
			{Source: "/primary/.git", Target: "/primary/.git"},
			{Source: "/worktree", Target: "/worktree"},
		},
	}, "image", []string{"true"}, "session", 1000, 1000, "developer", "developers", "/home/developer", "", false)
	if err != nil {
		t.Fatalf("BuildDockerArgs() error = %v", err)
	}

	var specifications []string
	for index, arg := range args {
		if arg == "--mount" {
			specifications = append(specifications, args[index+1])
		}
	}
	want := []string{
		"type=bind,source=/primary,target=/primary,bind-propagation=rprivate,readonly",
		"type=bind,source=/primary/.git,target=/primary/.git,bind-propagation=rprivate",
		"type=bind,source=/worktree,target=/worktree,bind-propagation=rprivate",
	}
	if !reflect.DeepEqual(specifications, want) {
		t.Errorf("mount specifications = %#v, want %#v", specifications, want)
	}
}

func TestBuildDockerArgsAttachesStdinWithoutForcingTTY(t *testing.T) {
	t.Parallel()

	args, err := BuildDockerArgs(
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"bash"},
		"session",
		1000,
		1000,
		"developer",
		"developers",
		"/home/developer",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("BuildDockerArgs() error = %v", err)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--interactive") {
		t.Fatalf("docker args do not attach stdin: %s", joined)
	}
	if strings.Contains(joined, "--tty") {
		t.Fatalf("docker args force a TTY for a non-terminal caller: %s", joined)
	}
}

func TestBuildDockerArgsMountsHostGitConfigReadOnly(t *testing.T) {
	t.Parallel()

	args, err := BuildDockerArgs(
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"true"},
		"session",
		1000,
		1000,
		"developer",
		"developers",
		"/home/developer profile",
		"/home/developer profile/.gitconfig",
		false,
	)
	if err != nil {
		t.Fatalf("BuildDockerArgs() error = %v", err)
	}

	var specifications []string
	for index, arg := range args {
		if arg == "--mount" {
			specifications = append(specifications, args[index+1])
		}
	}
	want := []string{
		"type=bind,source=/home/developer profile/.gitconfig," +
			"target=/home/developer profile/.gitconfig,bind-propagation=rprivate,readonly",
		"type=bind,source=/project,target=/project,bind-propagation=rprivate",
	}
	if !reflect.DeepEqual(specifications, want) {
		t.Errorf("mount specifications = %#v, want %#v", specifications, want)
	}
}

func TestBuildDockerExecArgsPreservesIdentityAndWorkingDirectory(t *testing.T) {
	t.Parallel()

	containerID := strings.Repeat("a", 64)
	probe := []string{"printf", "%s\\n", "value with spaces; $(not-a-shell)"}
	args, err := BuildDockerExecArgs(
		Plan{
			ProjectRoot: "/project",
			WorkingDir:  "/project/nested directory",
			Mounts:      []Mount{{Source: "/project", Target: "/project"}},
		},
		probe,
		containerID,
		1000,
		1001,
		"/home/developer profile",
		true,
	)
	if err != nil {
		t.Fatalf("BuildDockerExecArgs() error = %v", err)
	}

	want := []string{
		"exec",
		"--interactive",
		"--tty",
		"--user",
		"1000:1001",
		"--env",
		"HOME=/home/developer profile",
		"--workdir",
		"/project/nested directory",
		containerID,
		"printf",
		"%s\\n",
		"value with spaces; $(not-a-shell)",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("BuildDockerExecArgs() = %#v, want %#v", args, want)
	}
}

func TestBuildDockerArgsRejectsUnsupportedAccountNames(t *testing.T) {
	t.Parallel()

	plan := Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project",
		Mounts:      []Mount{{Source: "/project", Target: "/project"}},
	}
	tests := []struct {
		name      string
		hostUser  string
		hostGroup string
		want      string
	}{
		{name: "empty user", hostGroup: "developers", want: "host user name is empty"},
		{name: "unsafe user", hostUser: "bad:user", hostGroup: "developers", want: "host user name"},
		{name: "unsafe group", hostUser: "developer", hostGroup: "bad group", want: "host group name"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := BuildDockerArgs(
				plan,
				"image",
				[]string{"true"},
				"session",
				1000,
				1000,
				test.hostUser,
				test.hostGroup,
				"/home/developer",
				"",
				false,
			)
			if err == nil {
				t.Fatal("BuildDockerArgs() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildDockerArgs() error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestBuildDockerArgsRejectsNonCanonicalHostHome(t *testing.T) {
	t.Parallel()

	_, err := BuildDockerArgs(
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"true"},
		"session",
		1000,
		1000,
		"developer",
		"developers",
		"relative/home",
		"",
		false,
	)
	if err == nil {
		t.Fatal("BuildDockerArgs() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "host home directory") || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("BuildDockerArgs() error = %q, want non-absolute home error", err)
	}
}

func TestBuildDockerArgsRejectsNonCanonicalHostGitConfig(t *testing.T) {
	t.Parallel()

	_, err := BuildDockerArgs(
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"true"},
		"session",
		1000,
		1000,
		"developer",
		"developers",
		"/home/developer",
		"relative/.gitconfig",
		false,
	)
	if err == nil {
		t.Fatal("BuildDockerArgs() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "host Git config") || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("BuildDockerArgs() error = %q, want non-absolute Git config error", err)
	}
}

func TestDiscoverHostGitConfig(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	got, err := discoverHostGitConfig(home)
	if err != nil {
		t.Fatalf("discoverHostGitConfig() missing file error = %v", err)
	}
	if got != "" {
		t.Fatalf("discoverHostGitConfig() missing file = %q, want empty", got)
	}

	path := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(path, []byte("[user]\n\tname = Developer\n"), 0o600); err != nil {
		t.Fatalf("write Git config: %v", err)
	}
	got, err = discoverHostGitConfig(home)
	if err != nil {
		t.Fatalf("discoverHostGitConfig() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve Git config fixture: %v", err)
	}
	if got != want {
		t.Errorf("discoverHostGitConfig() = %q, want %q", got, want)
	}
}

func TestDiscoverHostGitConfigRejectsDirectory(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".gitconfig"), 0o700); err != nil {
		t.Fatalf("create Git config directory: %v", err)
	}
	_, err := discoverHostGitConfig(home)
	if err == nil {
		t.Fatal("discoverHostGitConfig() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("discoverHostGitConfig() error = %q, want regular-file error", err)
	}
}

func TestIsTerminalRejectsDevNull(t *testing.T) {
	t.Parallel()

	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = file.Close() })

	if isTerminal(file) {
		t.Fatalf("isTerminal(%s) = true, want false", os.DevNull)
	}
}

func TestDockerLaunchRunsPreflightThenContainer(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{
		outputs: []commandResult{
			{},
			{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
			{output: []byte(`[]`)},
		},
	}
	docker := testDocker(runner)

	err := docker.Launch(context.Background(), Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project",
		Mounts:      []Mount{{Source: "/project", Target: "/project"}},
	}, "image", []string{"echo", "safe"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}

	if len(runner.combinedCalls) != 3 {
		t.Fatalf("CombinedOutput calls = %d, want 3", len(runner.combinedCalls))
	}
	if !reflect.DeepEqual(runner.combinedCalls[0], []string{
		"docker", "container", "ls", "--quiet", "--no-trunc",
		"--filter", "label=codex-safe.session",
		"--filter", "label=codex-safe.project-path=/project",
		"--filter", "label=codex-safe.host-uid=1000",
	}) {
		t.Errorf("active-container lookup = %#v", runner.combinedCalls[0])
	}
	if !reflect.DeepEqual(runner.combinedCalls[1], []string{
		"docker", "info", "--format", "{{json .Runtimes}}",
	}) {
		t.Errorf("runtime preflight = %#v", runner.combinedCalls[1])
	}
	if !reflect.DeepEqual(runner.combinedCalls[2], []string{
		"docker", "image", "inspect", "image",
	}) {
		t.Errorf("image preflight = %#v", runner.combinedCalls[2])
	}
	if len(runner.runCalls) != 1 {
		t.Fatalf("Run calls = %d, want 1", len(runner.runCalls))
	}
}

func TestDockerLaunchExecutesInRunningProjectContainer(t *testing.T) {
	t.Parallel()

	containerID := strings.Repeat("b", 64)
	runner := &fakeCommandRunner{
		outputs: []commandResult{{output: []byte(containerID + "\n")}, {}},
	}
	docker := testDocker(runner)
	docker.TTY = true
	docker.NameGenerator = func() (string, error) {
		return "", errors.New("name generator must not run when a project container is active")
	}

	err := docker.Launch(
		context.Background(),
		Plan{
			ProjectRoot: "/project",
			WorkingDir:  "/project/nested",
			Mounts:      []Mount{{Source: "/project", Target: "/project"}},
		},
		"image",
		[]string{"make", "test"},
	)
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.combinedCalls) != 2 {
		t.Fatalf("CombinedOutput calls = %d, want lookup and readiness wait", len(runner.combinedCalls))
	}
	waitCall := runner.combinedCalls[1]
	if len(waitCall) != 6 || !reflect.DeepEqual(waitCall[:5], []string{
		"docker", "exec", containerID, "bash", "-c",
	}) || !strings.Contains(waitCall[5], "/run/codex-safe/ready") {
		t.Errorf("readiness wait = %#v", waitCall)
	}
	wantRun := []string{
		"docker",
		"exec",
		"--interactive",
		"--tty",
		"--user",
		"1000:1000",
		"--env",
		"HOME=/home/developer",
		"--workdir",
		"/project/nested",
		containerID,
		"make",
		"test",
	}
	if !reflect.DeepEqual(runner.runCalls, [][]string{wantRun}) {
		t.Errorf("Run calls = %#v, want %#v", runner.runCalls, [][]string{wantRun})
	}
}

func TestDockerLaunchRejectsMultipleRunningProjectContainers(t *testing.T) {
	t.Parallel()

	firstID := strings.Repeat("a", 64)
	secondID := strings.Repeat("b", 64)
	runner := &fakeCommandRunner{
		outputs: []commandResult{{output: []byte(firstID + "\n" + secondID + "\n")}},
	}
	docker := testDocker(runner)

	err := docker.Launch(
		context.Background(),
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"true"},
	)
	if err == nil {
		t.Fatal("Launch() error = nil, want an ambiguity error")
	}
	if !strings.Contains(err.Error(), "multiple active codex-safe containers manage project") {
		t.Fatalf("Launch() error = %q, want active-container ambiguity", err)
	}
	if len(runner.runCalls) != 0 {
		t.Fatalf("Run calls = %d, want 0", len(runner.runCalls))
	}
}

func TestDockerLaunchRejectsMissingSysboxWithoutRunning(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{
		outputs: []commandResult{{}, {output: []byte(`{"runc":{}}`)}},
	}
	docker := testDocker(runner)

	err := docker.Launch(
		context.Background(),
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"true"},
	)
	if err == nil {
		t.Fatal("Launch() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), `Docker runtime "sysbox-runc" is not registered`) {
		t.Fatalf("Launch() error = %q, want missing runtime", err)
	}
	if len(runner.runCalls) != 0 {
		t.Fatalf("Run calls = %d, want 0", len(runner.runCalls))
	}
}

func TestDockerLaunchPreservesExitError(t *testing.T) {
	t.Parallel()

	exitErr := fakeExitError{code: 37}
	runner := &fakeCommandRunner{
		outputs: []commandResult{
			{},
			{output: []byte(`{"sysbox-runc":{}}`)},
			{output: []byte(`[]`)},
		},
		runError: exitErr,
	}
	docker := testDocker(runner)

	err := docker.Launch(
		context.Background(),
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"false"},
	)
	if err == nil {
		t.Fatal("Launch() error = nil, want an error")
	}
	var got fakeExitError
	if !errors.As(err, &got) {
		t.Fatalf("Launch() error = %v, want fakeExitError", err)
	}
	if got.ExitCode() != 37 {
		t.Errorf("ExitCode() = %d, want 37", got.ExitCode())
	}
}

func TestDockerLaunchRejectsUnsupportedOS(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{}
	docker := testDocker(runner)
	docker.GOOS = "darwin"

	err := docker.Launch(
		context.Background(),
		Plan{ProjectRoot: "/project", WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"true"},
	)
	if err == nil {
		t.Fatal("Launch() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("Launch() error = %q, want Linux error", err)
	}
	if len(runner.combinedCalls) != 0 || len(runner.runCalls) != 0 {
		t.Fatal("unsupported OS should fail before Docker execution")
	}
}

func testDocker(runner CommandRunner) *Docker {
	return &Docker{
		Binary:    "docker",
		GOOS:      "linux",
		Runner:    runner,
		HostUID:   1000,
		HostGID:   1000,
		HostUser:  "developer",
		HostGroup: "developers",
		HostHome:  "/home/developer",
		NameGenerator: func() (string, error) {
			return "codex-safe-test", nil
		},
	}
}

type commandResult struct {
	output []byte
	err    error
}

type fakeCommandRunner struct {
	outputs       []commandResult
	combinedCalls [][]string
	runCalls      [][]string
	runError      error
}

func (runner *fakeCommandRunner) CombinedOutput(
	_ context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	runner.combinedCalls = append(runner.combinedCalls, append([]string{name}, args...))
	if len(runner.outputs) == 0 {
		return nil, errors.New("unexpected CombinedOutput call")
	}
	result := runner.outputs[0]
	runner.outputs = runner.outputs[1:]
	return result.output, result.err
}

func (runner *fakeCommandRunner) Run(
	_ context.Context,
	name string,
	args []string,
	_ io.Reader,
	_ io.Writer,
	_ io.Writer,
) error {
	runner.runCalls = append(runner.runCalls, append([]string{name}, args...))
	return runner.runError
}

type fakeExitError struct {
	code int
}

func (err fakeExitError) Error() string {
	return "exit failure"
}

func (err fakeExitError) ExitCode() int {
	return err.code
}
