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
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
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
	exitCode := cli.Run(context.Background(), testConfig(), []string{"--help"}, stdout, stderr, panicApp())
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
	exitCode := cli.Run(context.Background(), testConfig(), []string{"--unknown"}, new(bytes.Buffer), stderr, panicApp())
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
	exitCode := cli.Run(context.Background(), testConfig(), nil, new(bytes.Buffer), stderr, panicApp())
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
	app := panicApp()
	app.Discover = func(context.Context, string) (gitproject.Project, error) {
		return gitproject.Project{}, errors.New("Git unavailable")
	}
	exitCode := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr, app)
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
	app := panicApp()
	app.Discover = func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil }
	app.BuildPlan = func(gitproject.Project) (launcher.Plan, error) {
		return launcher.Plan{}, errors.New("mount plan invalid")
	}
	exitCode := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr, app)
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
	wantPlan := launcher.Plan{ProjectRoot: "/project", WorkingDir: "/project/nested"}
	fakeDocker := &recordingDocker{}
	var discoverPath string
	var builtFor gitproject.Project
	app := cli.App{
		Discover: func(_ context.Context, path string) (gitproject.Project, error) {
			discoverPath = path
			return wantProject, nil
		},
		BuildPlan: func(project gitproject.Project) (launcher.Plan, error) {
			builtFor = project
			return wantPlan, nil
		},
		Docker: fakeDocker,
	}

	exitCode := cli.Run(
		context.Background(),
		testConfig(),
		[]string{"--project", "/project/nested", "--image", "img", "cmd", "arg"},
		new(bytes.Buffer), new(bytes.Buffer), app,
	)

	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if discoverPath != "/project/nested" {
		t.Errorf("discover path = %q, want %q", discoverPath, "/project/nested")
	}
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("buildPlan project = %#v, want %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeDocker.plan, wantPlan) {
		t.Errorf("plan = %#v, want %#v", fakeDocker.plan, wantPlan)
	}
	if fakeDocker.image != "img" {
		t.Errorf("image = %q, want img", fakeDocker.image)
	}
	if !reflect.DeepEqual(fakeDocker.command, []string{"cmd", "arg"}) {
		t.Errorf("command = %#v, want [cmd arg]", fakeDocker.command)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	t.Parallel()
	app := cli.App{
		Discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		BuildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		Docker:    &recordingDocker{err: exitError{code: 42, message: "command failed"}},
	}
	exitCode := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), new(bytes.Buffer), app)
	if exitCode != 42 {
		t.Errorf("Run() = %d, want 42", exitCode)
	}
}

func panicApp() cli.App {
	return cli.App{
		Discover:  func(context.Context, string) (gitproject.Project, error) { panic("discover should not be called") },
		BuildPlan: func(gitproject.Project) (launcher.Plan, error) { panic("buildPlan should not be called") },
		Docker:    &recordingDocker{panicOnLaunch: true},
	}
}

type recordingDocker struct {
	plan          launcher.Plan
	image         string
	command       []string
	err           error
	panicOnLaunch bool
}

func (docker *recordingDocker) Launch(_ context.Context, plan launcher.Plan, image string, command []string) error {
	if docker.panicOnLaunch {
		panic("Launch should not be called")
	}
	docker.plan = plan
	docker.image = image
	docker.command = append([]string(nil), command...)
	return docker.err
}

type exitError struct {
	code    int
	message string
}

func (err exitError) Error() string { return err.message }
func (err exitError) ExitCode() int { return err.code }
