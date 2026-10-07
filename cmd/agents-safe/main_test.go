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
	"github.com/vkuptcov/agents-safe-environment/internal/launchcli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
	"github.com/vkuptcov/agents-safe-environment/internal/testutil/clitest"
)

func TestConfigRequiresCommandBeforeProjectResolution(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	if exit := cli.Run(context.Background(), config(), nil, new(bytes.Buffer), stderr, clitest.PanicDependencies()); exit != 2 {
		t.Fatalf("Run() = %d", exit)
	}
	if !strings.Contains(stderr.String(), "command is required") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunForwardsAgentsCommand(t *testing.T) {
	t.Parallel()
	recording := &clitest.RecordingLauncher{}
	deps := testCommandDependencies(recording)
	if exit := run(context.Background(), []string{"--image", "test:image", "--force-exec", "echo", "safe"},
		new(bytes.Buffer), new(bytes.Buffer), deps); exit != 0 {
		t.Fatalf("run() = %d", exit)
	}
	if !reflect.DeepEqual(recording.Command, []string{"echo", "safe"}) {
		t.Fatalf("command = %#v", recording.Command)
	}
	if recording.Image != "configured:image" || !recording.Options.ImageOverride || !recording.Options.ForceExec {
		t.Fatalf("launch image/options = %q, %#v", recording.Image, recording.Options)
	}
}

func TestRunRejectsMissingCommandBeforeConstructingLauncher(t *testing.T) {
	t.Parallel()
	deps := testCommandDependencies(&clitest.RecordingLauncher{})
	deps.newLauncher = func(string) (cli.Launcher, error) { panic("launcher must not be constructed") }
	if exit := run(context.Background(), nil, new(bytes.Buffer), new(bytes.Buffer), deps); exit != 2 {
		t.Fatalf("run() = %d, want 2", exit)
	}
}

func TestRunInitInitializesWithoutConstructingLauncher(t *testing.T) {
	t.Parallel()
	project := gitproject.Project{RequestedDir: "/project/nested", WorktreeRoot: "/project"}
	deps := testCommandDependencies(&clitest.RecordingLauncher{})
	deps.discover = func(context.Context, string) (gitproject.Project, error) { return project, nil }
	var initialized gitproject.Project
	deps.initialize = func(_ context.Context, got gitproject.Project, _ launchcli.HostCacheSelection) (initializationResult, error) {
		initialized = got
		return initializationResult{Initialization: projectenv.Initialization{
			Path: "/project/.agents-safe", CreatedCommon: true, CreatedConfig: true,
		}}, nil
	}
	deps.newLauncher = func(string) (cli.Launcher, error) { panic("launcher must not be constructed") }
	stdout := new(bytes.Buffer)
	if exit := run(context.Background(), []string{"init"}, stdout, new(bytes.Buffer), deps); exit != 0 {
		t.Fatalf("run() = %d", exit)
	}
	if initialized != project || !strings.Contains(stdout.String(), "Initialized /project/.agents-safe") {
		t.Fatalf("initialized = %#v; stdout = %q", initialized, stdout.String())
	}
}

func TestRunInitPassesUVSelectionToInitializer(t *testing.T) {
	t.Parallel()
	deps := testCommandDependencies(&clitest.RecordingLauncher{})
	var selection launchcli.HostCacheSelection
	deps.initialize = func(_ context.Context, _ gitproject.Project, got launchcli.HostCacheSelection) (initializationResult, error) {
		selection = got
		return initializationResult{Initialization: projectenv.Initialization{
			Path: "/project/.agents-safe", CreatedCommon: true, CreatedConfig: true,
		}}, nil
	}
	if exit := run(context.Background(), []string{"init", "--host-caches=uv"}, new(bytes.Buffer), new(bytes.Buffer), deps); exit != 0 {
		t.Fatalf("run() = %d", exit)
	}
	want := launchcli.HostCacheSelection{Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheUV}}
	if !reflect.DeepEqual(selection, want) {
		t.Fatalf("selection = %#v, want %#v", selection, want)
	}
}

func TestRunInitRejectsArgumentsBeforeDiscovery(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	if exit := run(context.Background(), []string{"init", "unexpected"}, new(bytes.Buffer), stderr,
		testCommandDependencies(&clitest.RecordingLauncher{})); exit != 2 {
		t.Fatalf("run() = %d", exit)
	}
	if !strings.Contains(stderr.String(), "positional arguments are not supported") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunInitRejectsInvalidHostCachesBeforeDiscovery(t *testing.T) {
	t.Parallel()
	deps := testCommandDependencies(&clitest.RecordingLauncher{})
	deps.discover = func(context.Context, string) (gitproject.Project, error) {
		panic("discovery must not run")
	}
	stderr := new(bytes.Buffer)
	if exit := run(context.Background(), []string{"init", "--host-caches=maven"}, new(bytes.Buffer), stderr, deps); exit != 2 {
		t.Fatalf("run() = %d", exit)
	}
	if !strings.Contains(stderr.String(), "--host-caches accepts only") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func testCommandDependencies(launcher cli.Launcher) commandDependencies {
	return commandDependencies{
		discover: func(context.Context, string) (gitproject.Project, error) {
			return gitproject.Project{RequestedDir: "/project", WorktreeRoot: "/project"}, nil
		},
		resolveConfig: func(
			_ gitproject.Project,
			_ string,
			overrides launchplan.Overrides,
		) (cli.ResolvedConfig, error) {
			return cli.ResolvedConfig{
				Plan:           launchplan.Plan{ProjectRoot: "/project", WorkingDir: "/project"},
				Image:          "configured:image",
				Options:        launchplan.Options{ImageOverride: overrides.ImageOverride, NoHostMCP: overrides.NoHostMCP},
				CodexArguments: []string{"unused"},
			}, nil
		},
		initialize: func(_ context.Context, project gitproject.Project, _ launchcli.HostCacheSelection) (initializationResult, error) {
			if project.WorktreeRoot == "" {
				return initializationResult{}, errors.New("missing worktree")
			}
			return initializationResult{Initialization: projectenv.Initialization{
				Path: "/project/.agents-safe", CreatedCommon: true, CreatedConfig: true,
			}}, nil
		},
		newLauncher: func(string) (cli.Launcher, error) { return launcher, nil },
	}
}

func TestRunInitReportsEachConfigFile(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		result initializationResult
		want   []string
	}{
		{
			result: initializationResult{Initialization: projectenv.Initialization{Path: "/p", CreatedCommon: true}},
			want:   []string{"Created common.toml", "Existing config.toml preserved"},
		},
		{
			result: initializationResult{Initialization: projectenv.Initialization{Path: "/p", CreatedConfig: true}},
			want:   []string{"Existing common.toml preserved", "No existing shared read-write host dependency caches"},
		},
	} {
		deps := testCommandDependencies(&clitest.RecordingLauncher{})
		deps.initialize = func(context.Context, gitproject.Project, launchcli.HostCacheSelection) (initializationResult, error) {
			return test.result, nil
		}
		stdout := new(bytes.Buffer)
		if exit := run(context.Background(), []string{"init"}, stdout, new(bytes.Buffer), deps); exit != 0 {
			t.Fatalf("run() = %d", exit)
		}
		for _, want := range test.want {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), want)
			}
		}
	}
}
