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

func TestRunRequiresProbeCommand(t *testing.T) {
	t.Parallel()

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	exitCode := run(context.Background(), nil, stdout, stderr, panicApplication())

	if exitCode != 2 {
		t.Errorf("run() = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "probe command is required after --") {
		t.Errorf("stderr = %q, want missing probe error", stderr.String())
	}
}

func TestRunParsesFlagsAndForwardsProbe(t *testing.T) {
	t.Parallel()

	wantProject := gitproject.Project{
		RequestedDir: "/project/nested",
		WorktreeRoot: "/project",
	}
	wantPlan := launcher.Plan{
		WorkingDir: "/project/nested",
		Mounts:     []launcher.Mount{{Source: "/project", Target: "/project"}},
	}
	fakeDocker := &recordingDocker{}
	app := application{
		discover: func(_ context.Context, path string) (gitproject.Project, error) {
			if path != "/project/nested" {
				t.Errorf("discover path = %q, want /project/nested", path)
			}
			return wantProject, nil
		},
		buildPlan: func(project gitproject.Project) (launcher.Plan, error) {
			if !reflect.DeepEqual(project, wantProject) {
				t.Errorf("buildPlan project = %#v, want %#v", project, wantProject)
			}
			return wantPlan, nil
		},
		docker: fakeDocker,
	}

	exitCode := run(
		context.Background(),
		[]string{
			"--project", "/project/nested",
			"--image", "test:image",
			"--",
			"printf", "%s", "value; $(not-shell)",
		},
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
	wantProbe := []string{"printf", "%s", "value; $(not-shell)"}
	if !reflect.DeepEqual(fakeDocker.probe, wantProbe) {
		t.Errorf("probe = %#v, want %#v", fakeDocker.probe, wantProbe)
	}
	if !reflect.DeepEqual(fakeDocker.plan, wantPlan) {
		t.Errorf("plan = %#v, want %#v", fakeDocker.plan, wantPlan)
	}
}

func TestRunReportsDiscoveryError(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	app := panicApplication()
	app.discover = func(context.Context, string) (gitproject.Project, error) {
		return gitproject.Project{}, errors.New("Git unavailable")
	}

	exitCode := run(context.Background(), []string{"--", "true"}, new(bytes.Buffer), stderr, app)

	if exitCode != 1 {
		t.Errorf("run() = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "Git unavailable") {
		t.Errorf("stderr = %q, want discovery error", stderr.String())
	}
}

func TestRunPropagatesProbeExitCode(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	app := application{
		discover: func(context.Context, string) (gitproject.Project, error) {
			return gitproject.Project{}, nil
		},
		buildPlan: func(gitproject.Project) (launcher.Plan, error) {
			return launcher.Plan{}, nil
		},
		docker: &recordingDocker{err: cliExitError{code: 42}},
	}

	exitCode := run(context.Background(), []string{"--", "false"}, new(bytes.Buffer), stderr, app)

	if exitCode != 42 {
		t.Errorf("run() = %d, want 42", exitCode)
	}
	if !strings.Contains(stderr.String(), "probe failed") {
		t.Errorf("stderr = %q, want probe error", stderr.String())
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
	probe         []string
	err           error
	panicOnLaunch bool
}

func (docker *recordingDocker) Launch(
	_ context.Context,
	plan launcher.Plan,
	image string,
	probe []string,
) error {
	if docker.panicOnLaunch {
		panic("Launch should not be called")
	}
	docker.plan = plan
	docker.image = image
	docker.probe = append([]string(nil), probe...)
	return docker.err
}

type cliExitError struct {
	code int
}

func (err cliExitError) Error() string {
	return "probe failed"
}

func (err cliExitError) ExitCode() int {
	return err.code
}
