package cli_test

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

func TestRunHelpAndUsageErrorsAvoidResolution(t *testing.T) {
	t.Parallel()
	config := testConfig()
	if exit := cli.Run(context.Background(), config, []string{"--help"}, new(bytes.Buffer), new(bytes.Buffer), clitest.PanicDependencies()); exit != 0 {
		t.Fatalf("help exit = %d, want 0", exit)
	}
	stderr := new(bytes.Buffer)
	if exit := cli.Run(context.Background(), config, nil, new(bytes.Buffer), stderr, clitest.PanicDependencies()); exit != 2 {
		t.Fatalf("missing command exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr.String(), "command is required") {
		t.Fatalf("stderr = %q, want usage error", stderr.String())
	}
}

func TestRunResolvesExplicitFlagsBeforeLaunch(t *testing.T) {
	t.Parallel()
	project := gitproject.Project{RequestedDir: "/project/nested", WorktreeRoot: "/project"}
	launcher := &clitest.RecordingLauncher{}
	var gotOverrides launchplan.Overrides
	var gotDefaultImage string
	exit := cli.Run(context.Background(), testConfig(),
		[]string{"--project", "/project/nested", "--image", "override:image", "--no-host-mcp=false", "cmd"},
		new(bytes.Buffer), new(bytes.Buffer), cli.Dependencies{
			Discover: func(_ context.Context, path string) (gitproject.Project, error) {
				if path != "/project/nested" {
					t.Errorf("path = %q", path)
				}
				return project, nil
			},
			ResolveConfig: func(
				gotProject gitproject.Project,
				defaultImage string,
				overrides launchplan.Overrides,
			) (cli.ResolvedConfig, error) {
				if gotProject != project {
					t.Errorf("project = %#v, want %#v", gotProject, project)
				}
				gotDefaultImage = defaultImage
				gotOverrides = overrides
				return testResolvedConfig(), nil
			},
			NewLauncher: func() (cli.Launcher, error) { return launcher, nil },
		})
	if exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	if want := (launchplan.Overrides{Image: "override:image", ImageOverride: true, NoHostMCPOverride: true}); gotOverrides != want {
		t.Fatalf("overrides = %#v, want %#v", gotOverrides, want)
	}
	if gotDefaultImage != "default:image" {
		t.Fatalf("default image = %q, want config default", gotDefaultImage)
	}
	if !reflect.DeepEqual(launcher.Command, []string{"configured", "cmd"}) {
		t.Fatalf("command = %#v", launcher.Command)
	}
	if launcher.Image != "resolved:image" || !launcher.Options.NoHostMCP {
		t.Fatalf("launch = image %q, options %#v", launcher.Image, launcher.Options)
	}
}

func TestRunReportsResolutionFailureAndWarnings(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	deps := cli.Dependencies{
		Discover: func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		ResolveConfig: func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
			return cli.ResolvedConfig{}, errors.New("bad config")
		},
		NewLauncher: func() (cli.Launcher, error) {
			return &clitest.RecordingLauncher{PanicOnLaunch: true}, nil
		},
	}
	if exit := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr, deps); exit != 1 {
		t.Fatalf("Run() = %d", exit)
	}
	if !strings.Contains(stderr.String(), "bad config") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stderr.Reset()
	deps.ResolveConfig = func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
		resolved := testResolvedConfig()
		resolved.Degradations = []launchplan.Degradation{{Role: "personal_skills"}}
		return resolved, nil
	}
	deps.NewLauncher = func() (cli.Launcher, error) { return &clitest.RecordingLauncher{}, nil }
	if exit := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr, deps); exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	if !strings.Contains(stderr.String(), "personal skills are unavailable") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunPrintsDegradationsInStableRoleOrder(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	deps := dependenciesFor(&clitest.RecordingLauncher{})
	deps.ResolveConfig = func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
		resolved := testResolvedConfig()
		resolved.Degradations = []launchplan.Degradation{
			{Role: "host_git_config"},
			{Role: "codex_home"},
			{Role: "personal_skills"},
			{Role: "host_mcp_channel"},
		}
		return resolved, nil
	}
	if exit := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr, deps); exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	got := stderr.String()
	last := -1
	for _, fragment := range []string{"host Git identity", "host Codex state", "personal skills", "host MCP"} {
		index := strings.Index(got, fragment)
		if index < 0 || index < last {
			t.Fatalf("warnings = %q, want stable order containing %q", got, fragment)
		}
		last = index
	}
}

func TestRunOrdersSyntheticCodexHomeWarningWithDegradations(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	config := testConfig()
	config.WarnWhenCodexHomeAbsent = true
	deps := dependenciesFor(&clitest.RecordingLauncher{})
	deps.ResolveConfig = func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
		resolved := testResolvedConfig()
		resolved.DefaultCodexHomeSet = false
		resolved.Degradations = []launchplan.Degradation{{Role: "personal_skills"}}
		return resolved, nil
	}
	if exit := cli.Run(context.Background(), config, []string{"cmd"}, new(bytes.Buffer), stderr, deps); exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	got := stderr.String()
	codex := strings.Index(got, "host Codex state")
	personal := strings.Index(got, "personal skills")
	if codex < 0 || personal < 0 || codex > personal {
		t.Fatalf("warnings = %q, want codex_home before personal_skills", got)
	}
}

func TestRunConstructsLauncherOnlyAfterResolution(t *testing.T) {
	t.Parallel()
	constructed := false
	deps := cli.Dependencies{
		Discover: func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		ResolveConfig: func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
			return cli.ResolvedConfig{}, errors.New("bad config")
		},
		NewLauncher: func() (cli.Launcher, error) {
			constructed = true
			return &clitest.RecordingLauncher{}, nil
		},
	}
	if exit := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), new(bytes.Buffer), deps); exit != 1 {
		t.Fatalf("Run() = %d", exit)
	}
	if constructed {
		t.Fatal("Run() constructed launcher after configuration resolution failed")
	}
}

func TestRunRejectsMissingLauncherDependency(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*cli.Dependencies){
		"factory absent": func(dependencies *cli.Dependencies) {
			dependencies.NewLauncher = nil
		},
		"factory returns nil": func(dependencies *cli.Dependencies) {
			dependencies.NewLauncher = func() (cli.Launcher, error) { return nil, nil }
		},
	}
	for name, arrange := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dependencies := dependenciesFor(&clitest.RecordingLauncher{})
			arrange(&dependencies)
			stderr := new(bytes.Buffer)
			if exit := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), stderr,
				dependencies); exit != 1 {
				t.Fatalf("Run() = %d, want 1", exit)
			}
			if !strings.Contains(stderr.String(), "launcher dependency is not configured") {
				t.Fatalf("stderr = %q, want missing launcher diagnostic", stderr.String())
			}
		})
	}
}

func TestRunPropagatesCommandExitCode(t *testing.T) {
	t.Parallel()
	launcher := &clitest.RecordingLauncher{Err: clitest.ExitError{Code: 23, Message: "command failed"}}
	if exit := cli.Run(context.Background(), testConfig(), []string{"cmd"}, new(bytes.Buffer), new(bytes.Buffer),
		dependenciesFor(launcher)); exit != 23 {
		t.Fatalf("Run() = %d, want 23", exit)
	}
}

func testConfig() cli.Config {
	return cli.Config{
		Name:         "test-safe",
		DefaultImage: "default:image",
		Usage:        "usage",
		ValidateInvocation: func(args []string) error {
			if len(args) == 0 {
				return errors.New("command is required")
			}
			return nil
		},
		BuildCommand: func(configured, invocation []string) []string {
			return append(append([]string(nil), configured...), invocation...)
		},
	}
}

func testResolvedConfig() cli.ResolvedConfig {
	return cli.ResolvedConfig{
		Plan:                launchplan.Plan{ProjectRoot: "/project", WorkingDir: "/project"},
		Image:               "resolved:image",
		Options:             launchplan.Options{NoHostMCP: true},
		CodexArguments:      []string{"configured"},
		DefaultCodexHomeSet: true,
	}
}

func dependenciesFor(launcher cli.Launcher) cli.Dependencies {
	return cli.Dependencies{
		Discover: func(context.Context, string) (gitproject.Project, error) {
			return gitproject.Project{}, nil
		},
		ResolveConfig: func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
			return testResolvedConfig(), nil
		},
		NewLauncher: func() (cli.Launcher, error) { return launcher, nil },
	}
}
