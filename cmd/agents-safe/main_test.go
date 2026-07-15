package main

import (
	"bytes"
	"context"
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
	if !strings.Contains(stdout.String(), "Usage: agents-safe") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunForwardsCommandWithoutSeparator(t *testing.T) {
	t.Parallel()

	fakeDocker := &recordingDocker{}
	app := application{
		discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		buildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		docker:    fakeDocker,
	}

	exitCode := run(
		context.Background(),
		[]string{"--project", "/project/nested", "--image", "test:image", "bash", "-c", "printf value"},
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
	wantCommand := []string{"bash", "-c", "printf value"}
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeDocker.command, wantCommand)
	}
}

func TestRunForwardsCommandAfterSeparator(t *testing.T) {
	t.Parallel()

	fakeDocker := &recordingDocker{}
	app := application{
		discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		buildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		docker:    fakeDocker,
	}

	exitCode := run(context.Background(), []string{"--", "bash", "-c", "printf value"}, new(bytes.Buffer), new(bytes.Buffer), app)

	if exitCode != 0 {
		t.Errorf("run() = %d, want 0", exitCode)
	}
	wantCommand := []string{"bash", "-c", "printf value"}
	if !reflect.DeepEqual(fakeDocker.command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeDocker.command, wantCommand)
	}
}

func TestRunRequiresCommand(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	exitCode := run(context.Background(), nil, new(bytes.Buffer), stderr, panicApplication())

	if exitCode != 2 {
		t.Errorf("run() = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "command is required") {
		t.Errorf("stderr = %q, want missing-command diagnostic", stderr.String())
	}
}

func TestRunPropagatesCommandExitCode(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	app := application{
		discover:  func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		buildPlan: func(gitproject.Project) (launcher.Plan, error) { return launcher.Plan{}, nil },
		docker:    &recordingDocker{err: cliExitError{code: 42}},
	}

	exitCode := run(context.Background(), []string{"false"}, new(bytes.Buffer), stderr, app)

	if exitCode != 42 {
		t.Errorf("run() = %d, want 42", exitCode)
	}
	if !strings.Contains(stderr.String(), "command failed") {
		t.Errorf("stderr = %q, want command error", stderr.String())
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
	image         string
	command       []string
	err           error
	panicOnLaunch bool
}

func (docker *recordingDocker) Launch(
	_ context.Context,
	_ launcher.Plan,
	image string,
	command []string,
) error {
	if docker.panicOnLaunch {
		panic("Launch should not be called")
	}
	docker.image = image
	docker.command = append([]string(nil), command...)
	return docker.err
}

type cliExitError struct {
	code int
}

func (err cliExitError) Error() string {
	return "command failed"
}

func (err cliExitError) ExitCode() int {
	return err.code
}
