package main

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
	if !strings.Contains(stdout.String(), ".agents-safe/Dockerfile") {
		t.Errorf("usage must document project image selection, got %q", stdout.String())
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
	if !fakeLauncher.Options.ImageOverride {
		t.Error("explicit image did not set ImageOverride")
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

func TestRunInitCreatesSampleWithoutConstructingLauncher(t *testing.T) {
	t.Parallel()
	wantProject := gitproject.Project{RequestedDir: "/project/nested", WorktreeRoot: "/project"}
	var discoverPath string
	var initializedRoot string
	dependencies := panicCommandDependencies()
	dependencies.discover = func(_ context.Context, path string) (gitproject.Project, error) {
		discoverPath = path
		return wantProject, nil
	}
	dependencies.createSample = func(root string) (string, error) {
		initializedRoot = root
		return "/project/.agents-safe/Dockerfile.sample", nil
	}
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	exitCode := run(
		context.Background(),
		[]string{"init", "--project", "/project/nested"},
		stdout,
		stderr,
		dependencies,
	)

	if exitCode != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if discoverPath != "/project/nested" {
		t.Errorf("discover path = %q, want /project/nested", discoverPath)
	}
	if initializedRoot != wantProject.WorktreeRoot {
		t.Errorf("initialized root = %q, want %q", initializedRoot, wantProject.WorktreeRoot)
	}
	if !strings.Contains(stdout.String(), "/project/.agents-safe/Dockerfile.sample") {
		t.Errorf("stdout = %q, want created sample path", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunInitHelpDoesNotUseRuntimeDependencies(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	exitCode := run(
		context.Background(),
		[]string{"init", "--help"},
		stdout,
		stderr,
		panicCommandDependencies(),
	)

	if exitCode != 0 {
		t.Fatalf("run() = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "Usage: agents-safe init") {
		t.Errorf("stdout = %q, want init usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunInitRejectsArgumentsBeforeDiscovery(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)

	exitCode := run(
		context.Background(),
		[]string{"init", "unexpected"},
		new(bytes.Buffer),
		stderr,
		panicCommandDependencies(),
	)

	if exitCode != 2 {
		t.Fatalf("run() = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "positional arguments are not supported") {
		t.Errorf("stderr = %q, want positional-argument diagnostic", stderr.String())
	}
}

func TestRunSeparatorPreservesContainerInitCommand(t *testing.T) {
	t.Parallel()
	recordingLauncher := &clitest.RecordingLauncher{}
	dependencies := commandDependencies{
		discover:        func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		buildLaunchPlan: func(gitproject.Project) (launchplan.Plan, error) { return launchplan.Plan{}, nil },
		createSample:    func(string) (string, error) { panic("CreateSample should not be called") },
		newLauncher:     func() (cli.Launcher, error) { return recordingLauncher, nil },
	}

	exitCode := run(
		context.Background(),
		[]string{"--", "init"},
		new(bytes.Buffer),
		new(bytes.Buffer),
		dependencies,
	)

	if exitCode != 0 {
		t.Fatalf("run() = %d, want 0", exitCode)
	}
	if !reflect.DeepEqual(recordingLauncher.Command, []string{"init"}) {
		t.Errorf("command = %#v, want [init]", recordingLauncher.Command)
	}
}

func TestRunInitReportsCreationError(t *testing.T) {
	t.Parallel()
	dependencies := panicCommandDependencies()
	dependencies.discover = func(context.Context, string) (gitproject.Project, error) {
		return gitproject.Project{WorktreeRoot: "/project"}, nil
	}
	dependencies.createSample = func(string) (string, error) {
		return "", errors.New("sample already exists")
	}
	stderr := new(bytes.Buffer)

	exitCode := run(context.Background(), []string{"init"}, new(bytes.Buffer), stderr, dependencies)

	if exitCode != 1 {
		t.Fatalf("run() = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "sample already exists") {
		t.Errorf("stderr = %q, want creation error", stderr.String())
	}
}

func panicCommandDependencies() commandDependencies {
	return commandDependencies{
		discover: func(context.Context, string) (gitproject.Project, error) {
			panic("Discover should not be called")
		},
		buildLaunchPlan: func(gitproject.Project) (launchplan.Plan, error) {
			panic("BuildLaunchPlan should not be called")
		},
		createSample: func(string) (string, error) {
			panic("CreateSample should not be called")
		},
		newLauncher: func() (cli.Launcher, error) {
			panic("NewLauncher should not be called")
		},
	}
}
