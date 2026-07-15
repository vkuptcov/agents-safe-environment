package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	exitCode := run(context.Background(), []string{"--help"}, stdout, stderr, panicApplication())

	if exitCode != 0 {
		t.Errorf("run() = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "Usage: codex-safe") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDefaultsToInteractiveCodex(t *testing.T) {
	t.Parallel()

	wantProject := gitproject.Project{RequestedDir: "/project", WorktreeRoot: "/project"}
	wantPlan := launcher.Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project",
		Mounts:      []launcher.Mount{{Source: "/project", Target: "/project"}},
	}
	fakeDocker := &recordingDocker{}
	var builtFor gitproject.Project
	app := application{
		discover: func(_ context.Context, path string) (gitproject.Project, error) {
			if path != "." {
				t.Errorf("discover path = %q, want default \".\"", path)
			}
			return wantProject, nil
		},
		buildPlan: func(project gitproject.Project) (launcher.Plan, error) {
			builtFor = project
			return wantPlan, nil
		},
		docker: fakeDocker,
	}

	exitCode := run(context.Background(), nil, new(bytes.Buffer), new(bytes.Buffer), app)

	if exitCode != 0 {
		t.Errorf("run() = %d, want 0", exitCode)
	}
	wantCommand := []string{launcher.CodexBinaryPath}
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want interactive Codex %#v", fakeDocker.command, wantCommand)
	}
	if fakeDocker.image != defaultImage {
		t.Errorf("image = %q, want %q", fakeDocker.image, defaultImage)
	}
	// The discovered project must reach buildPlan, and the plan it builds must be the plan handed
	// to docker.Launch: otherwise the outer container launches with no mounts and a wrong workdir.
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("buildPlan project = %#v, want discovered %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeDocker.plan, wantPlan) {
		t.Errorf("plan forwarded to Launch = %#v, want %#v", fakeDocker.plan, wantPlan)
	}
}

func TestRunForwardsCodexArguments(t *testing.T) {
	t.Parallel()

	fakeDocker := &recordingDocker{}
	app := application{
		discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		buildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		docker:    fakeDocker,
	}

	exitCode := run(
		context.Background(),
		[]string{"--project", "/project/nested", "--image", "test:image", "--", "exec", "--model", "gpt-5"},
		new(bytes.Buffer),
		new(bytes.Buffer),
		app,
	)

	if exitCode != 0 {
		t.Errorf("run() = %d, want 0", exitCode)
	}
	if fakeDocker.image != "test:image" {
		t.Errorf("image = %q, want test:image", fakeDocker.image)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "exec", "--model", "gpt-5"}
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeDocker.command, wantCommand)
	}
}

// TestRunNeverRunsArbitraryExecutable proves that a would-be executable after -- becomes a Codex
// argument. The image-owned Codex path is always command[0]; the launcher never runs another program.
func TestRunNeverRunsArbitraryExecutable(t *testing.T) {
	t.Parallel()

	fakeDocker := &recordingDocker{}
	app := application{
		discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		buildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		docker:    fakeDocker,
	}

	exitCode := run(
		context.Background(),
		[]string{"--", "/bin/sh", "-c", "rm -rf /; $(malicious)"},
		new(bytes.Buffer),
		new(bytes.Buffer),
		app,
	)

	if exitCode != 0 {
		t.Errorf("run() = %d, want 0", exitCode)
	}
	if len(fakeDocker.command) == 0 || fakeDocker.command[0] != launcher.CodexBinaryPath {
		t.Fatalf("command[0] = %#v, want image-owned Codex path", fakeDocker.command)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "/bin/sh", "-c", "rm -rf /; $(malicious)"}
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want the executable forwarded as a Codex argument %#v", fakeDocker.command, wantCommand)
	}
}

func TestRunReportsDiscoveryError(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	app := panicApplication()
	app.discover = func(context.Context, string) (gitproject.Project, error) {
		return gitproject.Project{}, errors.New("Git unavailable")
	}

	exitCode := run(context.Background(), nil, new(bytes.Buffer), stderr, app)

	if exitCode != 1 {
		t.Errorf("run() = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "Git unavailable") {
		t.Errorf("stderr = %q, want discovery error", stderr.String())
	}
}

func TestRunPropagatesCodexExitCode(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	app := application{
		discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		buildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		docker:    &recordingDocker{err: cliExitError{code: 42}},
	}

	exitCode := run(context.Background(), nil, new(bytes.Buffer), stderr, app)

	if exitCode != 42 {
		t.Errorf("run() = %d, want 42", exitCode)
	}
	if !strings.Contains(stderr.String(), "codex failed") {
		t.Errorf("stderr = %q, want Codex error", stderr.String())
	}
}

func panicApplication() application {
	return application{
		discover: func(context.Context, string) (gitproject.Project, error) {
			panic("discover should not be called")
		},
		buildPlan: func(gitproject.Project) (launcher.Plan, error) {
			panic("buildPlan should not be called")
		},
		docker: &recordingDocker{panicOnLaunch: true},
	}
}

type recordingDocker struct {
	plan          launcher.Plan
	image         string
	command       []string
	err           error
	panicOnLaunch bool
}

func (docker *recordingDocker) Launch(
	_ context.Context,
	plan launcher.Plan,
	image string,
	command []string,
) error {
	if docker.panicOnLaunch {
		panic("Launch should not be called")
	}
	docker.plan = plan
	docker.image = image
	docker.command = append([]string(nil), command...)
	return docker.err
}

type cliExitError struct {
	code int
}

func (err cliExitError) Error() string {
	return "codex failed"
}

func (err cliExitError) ExitCode() int {
	return err.code
}
