package main

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/testutil/clitest"
)

func TestConfigHelp(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), config(), []string{"--help"}, stdout, stderr, clitest.PanicApp())
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "Usage: codex-safe") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestConfigDefaultsToInteractiveCodex(t *testing.T) {
	t.Parallel()
	wantProject := gitproject.Project{RequestedDir: "/project", WorktreeRoot: "/project"}
	wantPlan := launcher.Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project",
		Mounts:      []launcher.Mount{{Source: "/project", Target: "/project"}},
	}
	fakeDocker := &clitest.RecordingDocker{}
	var builtFor gitproject.Project
	app := cli.App{
		Discover: func(_ context.Context, path string) (gitproject.Project, error) {
			if path != "." {
				t.Errorf("discover path = %q, want default \".\"", path)
			}
			return wantProject, nil
		},
		BuildPlan: func(project gitproject.Project) (launcher.Plan, error) {
			builtFor = project
			return wantPlan, nil
		},
		Docker: fakeDocker,
	}

	exitCode := cli.Run(context.Background(), config(), nil, new(bytes.Buffer), new(bytes.Buffer), app)

	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	wantCommand := []string{launcher.CodexBinaryPath}
	if !reflect.DeepEqual(fakeDocker.Command, wantCommand) {
		t.Errorf("command = %#v, want interactive Codex %#v", fakeDocker.Command, wantCommand)
	}
	if fakeDocker.Image != defaultImage {
		t.Errorf("image = %q, want %q", fakeDocker.Image, defaultImage)
	}
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("buildPlan project = %#v, want discovered %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeDocker.Plan, wantPlan) {
		t.Errorf("plan forwarded to Launch = %#v, want %#v", fakeDocker.Plan, wantPlan)
	}
}

func TestConfigForwardsCodexArguments(t *testing.T) {
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
		[]string{"--project", "/project/nested", "--image", "test:image", "--", "exec", "--model", "gpt-5"},
		new(bytes.Buffer), new(bytes.Buffer), app,
	)
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if fakeDocker.Image != "test:image" {
		t.Errorf("image = %q, want test:image", fakeDocker.Image)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "exec", "--model", "gpt-5"}
	if !reflect.DeepEqual(fakeDocker.Command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeDocker.Command, wantCommand)
	}
}

// TestConfigNeverRunsArbitraryExecutable proves that a would-be executable after -- becomes a Codex
// argument. The image-owned Codex path is always command[0]; the launcher never runs another program.
func TestConfigNeverRunsArbitraryExecutable(t *testing.T) {
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
		[]string{"--", "/bin/sh", "-c", "rm -rf /; $(malicious)"},
		new(bytes.Buffer), new(bytes.Buffer), app,
	)
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "/bin/sh", "-c", "rm -rf /; $(malicious)"}
	if !reflect.DeepEqual(fakeDocker.Command, wantCommand) {
		t.Errorf("command = %#v, want the executable forwarded as a Codex argument %#v", fakeDocker.Command, wantCommand)
	}
}
