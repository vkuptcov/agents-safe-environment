package main

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/testutil/clitest"
)

func TestConfigHelp(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), config(), []string{"--help"}, stdout, stderr, clitest.PanicDependencies())
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "Usage: agents-safe") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
	// The usage is hand-written, not generated from the flag set, so it must advertise the flag or
	// --help would describe an incomplete interface.
	if !strings.Contains(stdout.String(), "--no-host-mcp") {
		t.Errorf("usage must document --no-host-mcp, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestConfigForwardsCommandWithoutSeparator(t *testing.T) {
	t.Parallel()
	wantProject := gitproject.Project{RequestedDir: "/project/nested", WorktreeRoot: "/project"}
	wantLaunchPlan := launchplan.Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project/nested",
		Mounts:      []launchplan.BindMount{{Source: "/project", Target: "/project"}},
	}
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
		config(),
		[]string{"--project", "/project/nested", "--image", "test:image", "bash", "-c", "printf value"},
		new(bytes.Buffer), new(bytes.Buffer), dependencies,
	)

	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if fakeLauncher.Image != "test:image" {
		t.Errorf("image = %q, want test:image", fakeLauncher.Image)
	}
	wantCommand := []string{"bash", "-c", "printf value"}
	if !reflect.DeepEqual(fakeLauncher.Command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeLauncher.Command, wantCommand)
	}
	// The --project value must reach discover, the discovered project must reach BuildLaunchPlan, and that
	// launch plan must reach Launcher.Launch: otherwise the container starts with the wrong project mounts.
	if discoverPath != "/project/nested" {
		t.Errorf("discover path = %q, want %q", discoverPath, "/project/nested")
	}
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("BuildLaunchPlan project = %#v, want discovered %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeLauncher.LaunchPlan, wantLaunchPlan) {
		t.Errorf("launch plan forwarded to Launch = %#v, want %#v", fakeLauncher.LaunchPlan, wantLaunchPlan)
	}
}

func TestConfigForwardsCommandAfterSeparator(t *testing.T) {
	t.Parallel()
	fakeLauncher := &clitest.RecordingLauncher{}
	dependencies := cli.Dependencies{
		Discover:        func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		BuildLaunchPlan: func(gitproject.Project) (launchplan.Plan, error) { return launchplan.Plan{}, nil },
		Launcher:        fakeLauncher,
	}
	exitCode := cli.Run(
		context.Background(),
		config(),
		[]string{"--", "bash", "-c", "printf value"},
		new(bytes.Buffer), new(bytes.Buffer), dependencies,
	)
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	wantCommand := []string{"bash", "-c", "printf value"}
	if !reflect.DeepEqual(fakeLauncher.Command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeLauncher.Command, wantCommand)
	}
}

func TestConfigRequiresCommand(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), config(), nil, new(bytes.Buffer), stderr, clitest.PanicDependencies())
	if exitCode != 2 {
		t.Errorf("Run() = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "command is required") {
		t.Errorf("stderr = %q, want missing-command diagnostic", stderr.String())
	}
}
