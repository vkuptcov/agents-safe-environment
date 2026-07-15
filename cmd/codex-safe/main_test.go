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
)

func TestConfigHelp(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	exitCode := cli.Run(context.Background(), config(), []string{"--help"}, stdout, stderr, panicApp())
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
	fakeDocker := &recordingDocker{}
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
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want interactive Codex %#v", fakeDocker.command, wantCommand)
	}
	if fakeDocker.image != defaultImage {
		t.Errorf("image = %q, want %q", fakeDocker.image, defaultImage)
	}
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("buildPlan project = %#v, want discovered %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeDocker.plan, wantPlan) {
		t.Errorf("plan forwarded to Launch = %#v, want %#v", fakeDocker.plan, wantPlan)
	}
}

func TestConfigForwardsCodexArguments(t *testing.T) {
	t.Parallel()
	fakeDocker := &recordingDocker{}
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
	if fakeDocker.image != "test:image" {
		t.Errorf("image = %q, want test:image", fakeDocker.image)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "exec", "--model", "gpt-5"}
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeDocker.command, wantCommand)
	}
}

// TestConfigNeverRunsArbitraryExecutable proves that a would-be executable after -- becomes a Codex
// argument. The image-owned Codex path is always command[0]; the launcher never runs another program.
func TestConfigNeverRunsArbitraryExecutable(t *testing.T) {
	t.Parallel()
	fakeDocker := &recordingDocker{}
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
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want the executable forwarded as a Codex argument %#v", fakeDocker.command, wantCommand)
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
