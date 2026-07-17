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
	if !strings.Contains(stdout.String(), "Usage: codex-safe") {
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

func TestConfigDefaultsToInteractiveCodex(t *testing.T) {
	t.Parallel()
	wantProject := gitproject.Project{RequestedDir: "/project", WorktreeRoot: "/project"}
	wantLaunchPlan := launchplan.Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project",
		Mounts:      []launchplan.BindMount{{Source: "/project", Target: "/project"}},
	}
	fakeLauncher := &clitest.RecordingLauncher{}
	var builtFor gitproject.Project
	dependencies := cli.Dependencies{
		Discover: func(_ context.Context, path string) (gitproject.Project, error) {
			if path != "." {
				t.Errorf("discover path = %q, want default \".\"", path)
			}
			return wantProject, nil
		},
		BuildLaunchPlan: func(project gitproject.Project) (launchplan.Plan, error) {
			builtFor = project
			return wantLaunchPlan, nil
		},
		Launcher: fakeLauncher,
	}

	exitCode := cli.Run(context.Background(), config(), nil, new(bytes.Buffer), new(bytes.Buffer), dependencies)

	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "--sandbox", "danger-full-access"}
	if !reflect.DeepEqual(fakeLauncher.Command, wantCommand) {
		t.Errorf("command = %#v, want interactive Codex %#v", fakeLauncher.Command, wantCommand)
	}
	if fakeLauncher.Image != defaultImage {
		t.Errorf("image = %q, want %q", fakeLauncher.Image, defaultImage)
	}
	if !reflect.DeepEqual(builtFor, wantProject) {
		t.Errorf("BuildLaunchPlan project = %#v, want discovered %#v", builtFor, wantProject)
	}
	if !reflect.DeepEqual(fakeLauncher.LaunchPlan, wantLaunchPlan) {
		t.Errorf("launch plan forwarded to Launch = %#v, want %#v", fakeLauncher.LaunchPlan, wantLaunchPlan)
	}
}

func TestConfigForwardsCodexArguments(t *testing.T) {
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
		[]string{"--project", "/project/nested", "--image", "test:image", "--", "exec", "--model", "gpt-5"},
		new(bytes.Buffer), new(bytes.Buffer), dependencies,
	)
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	if fakeLauncher.Image != "test:image" {
		t.Errorf("image = %q, want test:image", fakeLauncher.Image)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "--sandbox", "danger-full-access", "exec", "--model", "gpt-5"}
	if !reflect.DeepEqual(fakeLauncher.Command, wantCommand) {
		t.Errorf("command = %#v, want %#v", fakeLauncher.Command, wantCommand)
	}
}

// TestConfigNeverRunsArbitraryExecutable proves that a would-be executable after -- becomes a Codex
// argument. The image-owned Codex path is always command[0]; the launcher never runs another program.
func TestConfigNeverRunsArbitraryExecutable(t *testing.T) {
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
		[]string{"--", "/bin/sh", "-c", "rm -rf /; $(malicious)"},
		new(bytes.Buffer), new(bytes.Buffer), dependencies,
	)
	if exitCode != 0 {
		t.Errorf("Run() = %d, want 0", exitCode)
	}
	wantCommand := []string{launcher.CodexBinaryPath, "--sandbox", "danger-full-access", "/bin/sh", "-c", "rm -rf /; $(malicious)"}
	if !reflect.DeepEqual(fakeLauncher.Command, wantCommand) {
		t.Errorf("command = %#v, want the executable forwarded as a Codex argument %#v", fakeLauncher.Command, wantCommand)
	}
}
