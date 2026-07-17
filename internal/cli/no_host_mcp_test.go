package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/testutil/clitest"
)

func forwardingDependencies(launcher *clitest.RecordingLauncher) cli.Dependencies {
	return cli.Dependencies{
		Discover: func(context.Context, string) (gitproject.Project, error) {
			return gitproject.Project{RequestedDir: "/project", WorktreeRoot: "/project"}, nil
		},
		BuildLaunchPlan: func(gitproject.Project) (launchplan.Plan, error) {
			return launchplan.Plan{ProjectRoot: "/project", WorkingDir: "/project"}, nil
		},
		Launcher: launcher,
	}
}

func TestRunDefaultsToHostMCPEnabled(t *testing.T) {
	t.Parallel()
	launcher := &clitest.RecordingLauncher{}
	exit := cli.Run(context.Background(), testConfig(), []string{"cmd"},
		new(bytes.Buffer), new(bytes.Buffer), forwardingDependencies(launcher))
	if exit != 0 {
		t.Fatalf("Run() = %d, want 0", exit)
	}
	if launcher.Options.NoHostMCP {
		t.Fatal("host MCP forwarding must be on by default, so a shared config.toml behaves the same on both sides")
	}
}

func TestRunForwardsNoHostMCP(t *testing.T) {
	t.Parallel()
	launcher := &clitest.RecordingLauncher{}
	exit := cli.Run(context.Background(), testConfig(), []string{"--no-host-mcp", "cmd"},
		new(bytes.Buffer), new(bytes.Buffer), forwardingDependencies(launcher))
	if exit != 0 {
		t.Fatalf("Run() = %d, want 0", exit)
	}
	if !launcher.Options.NoHostMCP {
		t.Fatal("--no-host-mcp must reach the launcher, or the flag would silently do nothing")
	}
}
