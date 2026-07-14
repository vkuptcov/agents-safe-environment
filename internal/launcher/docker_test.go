package launcher

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestBuildDockerArgsUsesSysboxAndPreservesProbe(t *testing.T) {
	t.Parallel()

	plan := Plan{
		WorkingDir: "/sources/feature worktree/nested",
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
		"--env",
		"CODEX_SAFE_HOST_UID=1000",
		"--env",
		"CODEX_SAFE_HOST_GID=1000",
		"--env",
		"CODEX_SAFE_HOST_USER=developer",
		"--env",
		"CODEX_SAFE_HOST_GROUP=developers",
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
		WorkingDir: "/worktree",
		Mounts: []Mount{
			{Source: "/primary", Target: "/primary", ReadOnly: true},
			{Source: "/primary/.git", Target: "/primary/.git"},
			{Source: "/worktree", Target: "/worktree"},
		},
	}, "image", []string{"true"}, "session", 1000, 1000, "developer", "developers", false)
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
		Plan{WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
		"image",
		[]string{"bash"},
		"session",
		1000,
		1000,
		"developer",
		"developers",
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

func TestBuildDockerArgsRejectsUnsupportedAccountNames(t *testing.T) {
	t.Parallel()

	plan := Plan{WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}}
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
			{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
			{output: []byte(`[]`)},
		},
	}
	docker := testDocker(runner)

	err := docker.Launch(context.Background(), Plan{
		WorkingDir: "/project",
		Mounts:     []Mount{{Source: "/project", Target: "/project"}},
	}, "image", []string{"echo", "safe"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}

	if len(runner.combinedCalls) != 2 {
		t.Fatalf("CombinedOutput calls = %d, want 2", len(runner.combinedCalls))
	}
	if !reflect.DeepEqual(runner.combinedCalls[0], []string{
		"docker", "info", "--format", "{{json .Runtimes}}",
	}) {
		t.Errorf("runtime preflight = %#v", runner.combinedCalls[0])
	}
	if !reflect.DeepEqual(runner.combinedCalls[1], []string{
		"docker", "image", "inspect", "image",
	}) {
		t.Errorf("image preflight = %#v", runner.combinedCalls[1])
	}
	if len(runner.runCalls) != 1 {
		t.Fatalf("Run calls = %d, want 1", len(runner.runCalls))
	}
}

func TestDockerLaunchRejectsMissingSysboxWithoutRunning(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{
		outputs: []commandResult{{output: []byte(`{"runc":{}}`)}},
	}
	docker := testDocker(runner)

	err := docker.Launch(
		context.Background(),
		Plan{WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
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
			{output: []byte(`{"sysbox-runc":{}}`)},
			{output: []byte(`[]`)},
		},
		runError: exitErr,
	}
	docker := testDocker(runner)

	err := docker.Launch(
		context.Background(),
		Plan{WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
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
		Plan{WorkingDir: "/project", Mounts: []Mount{{Source: "/project", Target: "/project"}}},
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
