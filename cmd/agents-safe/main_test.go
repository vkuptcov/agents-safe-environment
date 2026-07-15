package main

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/cli/clitest"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

func TestConfigHelp(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), config(), []string{"--help"}, stdout, stderr, clitest.PanicApp())
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "Usage: agents-safe") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestConfigForwardsCommandWithoutSeparator(t *testing.T) {
	t.Parallel()
	wantProject := gitproject.Project{RequestedDir: "/project/nested", WorktreeRoot: "/project"}
	wantPlan := launcher.Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project/nested",
		Mounts:      []launcher.Mount{{Source: "/project", Target: "/project"}},
	}
	fakeDocker := &clitest.RecordingDocker{}
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
		config(),
		[]string{"--project", "/project/nested", "--image", "test:image", "bash", "-c", "printf value"},
		new(bytes.Buffer), new(bytes.Buffer), app,
	)

	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if fakeDocker.Image != "test:image" {
		t.Errorf("image = %q, want test:image", fakeDocker.Image)
	}
	wantCommand := []string{"bash", "-c", "printf value"}
	if !reflect.DeepEqual(fakeDocker.Command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeDocker.Command, wantCommand)
	}
	// The --project value must reach discover, the discovered project must reach buildPlan, and that
	// plan must reach docker.Launch: otherwise the container launches with the wrong project mounts.
	if discoverPath != "/project/nested" {
		t.Errorf("discover path = %q, want %q", discoverPath, "/project/nested")
	}
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("buildPlan project = %#v, want discovered %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeDocker.Plan, wantPlan) {
		t.Errorf("plan forwarded to Launch = %#v, want %#v", fakeDocker.Plan, wantPlan)
	}
}

func TestConfigForwardsCommandAfterSeparator(t *testing.T) {
	t.Parallel()
	fakeDocker := &clitest.RecordingDocker{}
	app := cli.App{
		Discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		BuildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		Docker:    fakeDocker,
	}
	exitCode := cli.Run(
		context.Background(),
		config(),
		[]string{"--", "bash", "-c", "printf value"},
		new(bytes.Buffer), new(bytes.Buffer), app,
	)
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	wantCommand := []string{"bash", "-c", "printf value"}
	if !reflect.DeepEqual(fakeDocker.Command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeDocker.Command, wantCommand)
	}
}

func TestConfigRequiresCommand(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), config(), nil, new(bytes.Buffer), stderr, clitest.PanicApp())
	if exitCode != 2 {
		t.Errorf("Run() = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "command is required") {
		t.Errorf("stderr = %q, want missing-command diagnostic", stderr.String())
	}
}
