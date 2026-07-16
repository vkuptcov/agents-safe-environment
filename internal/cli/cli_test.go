package cli_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/testutil/clitest"
)

func testConfig() cli.Config {
	return cli.Config{
		Name:         "test-cli",
		DefaultImage: "default:image",
		Usage:        "Usage: test-cli [--project PATH] [--image REF] [--] COMMAND",
		BuildCommand: func(args []string) ([]string, error) {
			if len(args) == 0 {
				return nil, errors.New("command is required")
			}
			return args, nil
		},
	}
}

func TestRunHelp(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), testConfig(), []string{"--help"}, stdout, stderr, clitest.PanicDependencies())
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "Usage: test-cli") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunReportsFlagError(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), testConfig(), []string{"--unknown"}, new(bytes.Buffer), stderr, clitest.PanicDependencies())
	if exitCode != 2 {
		t.Errorf("Run() = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "Usage: test-cli") {
		t.Errorf("stderr = %q, want usage", stderr.String())
	}
}

func TestRunReportsBuildCommandError(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), testConfig(), nil, new(bytes.Buffer), stderr, clitest.PanicDependencies())
	if exitCode != 2 {
		t.Errorf("Run() = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "command is required") {
		t.Errorf("stderr = %q, want build-command diagnostic", stderr.String())
	}
}

func TestRunReportsDiscoveryError(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	dependencies := clitest.PanicDependencies()
	dependencies.Discover = func(context.Context, string) (gitproject.Project, error) {
		return gitproject.Project{}, errors.New("Git unavailable")
	}
	exitCode := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr, dependencies)
	if exitCode != 1 {
		t.Errorf("Run() = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "Git unavailable") {
		t.Errorf("stderr = %q, want discovery error", stderr.String())
	}
}

func TestRunReportsPlanError(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	dependencies := clitest.PanicDependencies()
	dependencies.Discover = func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil }
	dependencies.BuildLaunchPlan = func(gitproject.Project) (launchplan.Plan, error) {
		return launchplan.Plan{}, errors.New("mount plan invalid")
	}
	exitCode := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr, dependencies)
	if exitCode != 1 {
		t.Errorf("Run() = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "mount plan invalid") {
		t.Errorf("stderr = %q, want plan error", stderr.String())
	}
}

func TestRunForwardsProjectPlanAndCommand(t *testing.T) {
	t.Parallel()
	wantProject := gitproject.Project{RequestedDir: "/project/nested", WorktreeRoot: "/project"}
	wantLaunchPlan := launchplan.Plan{ProjectRoot: "/project", WorkingDir: "/project/nested"}
	fakeLauncher := &clitest.RecordingLauncher{}
	var discoverPath string
	var builtFor gitproject.Project
	dependencies := cli.Dependencies{
		Discover: func(_ context.Context, path string) (gitproject.Project, error) {
			discoverPath = path
			return wantProject, nil
		},
		BuildLaunchPlan: func(project gitproject.Project) (launchplan.Plan, error) {
			builtFor = project
			return wantLaunchPlan, nil
		},
		Launcher: fakeLauncher,
	}

	exitCode := cli.Run(
		context.Background(),
		testConfig(),
		[]string{"--project", "/project/nested", "--image", "img", "cmd", "arg"},
		new(bytes.Buffer), new(bytes.Buffer), dependencies,
	)

	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if discoverPath != "/project/nested" {
		t.Errorf("discover path = %q, want %q", discoverPath, "/project/nested")
	}
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("BuildLaunchPlan project = %#v, want %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeLauncher.LaunchPlan, wantLaunchPlan) {
		t.Errorf("plan = %#v, want %#v", fakeLauncher.LaunchPlan, wantLaunchPlan)
	}
	if fakeLauncher.Image != "img" {
		t.Errorf("image = %q, want img", fakeLauncher.Image)
	}
	if !reflect.DeepEqual(fakeLauncher.Command, []string{"cmd", "arg"}) {
		t.Errorf("command = %#v, want [cmd arg]", fakeLauncher.Command)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	t.Parallel()
	dependencies := cli.Dependencies{
		Discover:        func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		BuildLaunchPlan: func(gitproject.Project) (launchplan.Plan, error) { return launchplan.Plan{}, nil },
		Launcher:        &clitest.RecordingLauncher{Err: clitest.ExitError{Code: 42, Message: "command failed"}},
	}
	exitCode := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), new(bytes.Buffer), dependencies)
	if exitCode != 42 {
		t.Errorf("Run() = %d, want 42", exitCode)
	}
}
