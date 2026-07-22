package main

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/testutil/clitest"
)

func TestConfigBuildsConfiguredClaudeCommand(t *testing.T) {
	t.Parallel()
	recording := &clitest.RecordingLauncher{}
	if exit := cli.Run(context.Background(), config(), []string{"--", "--permission-mode", "plan"},
		new(bytes.Buffer), new(bytes.Buffer), claudeDependencies(recording)); exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	want := []string{launcher.ClaudeBinaryPath, "--model", "opus", "--permission-mode", "plan"}
	if !reflect.DeepEqual(recording.Command, want) {
		t.Fatalf("command = %#v, want %#v", recording.Command, want)
	}
}

func TestConfigWarnsWhenHostClaudeStateIsAbsent(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	if exit := cli.Run(context.Background(), config(), nil, new(bytes.Buffer), stderr,
		claudeDependencies(&clitest.RecordingLauncher{})); exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	if !strings.Contains(stderr.String(), "host Claude Code state") || !strings.Contains(stderr.String(), "ephemeral") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunUpdateBypassesProjectDiscovery(t *testing.T) {
	t.Parallel()
	called := false
	dependencies := commandDependencies{
		launch: cli.Dependencies{Discover: func(context.Context, string) (gitproject.Project, error) {
			t.Fatal("update must not discover a Git project")
			return gitproject.Project{}, nil
		}},
		update: func(_ context.Context, image string, _, _ io.Writer) error {
			called = true
			if image != defaultImage {
				t.Fatalf("image = %q, want %q", image, defaultImage)
			}
			return nil
		},
	}
	if exit := run(context.Background(), []string{"update"}, io.Discard, io.Discard, dependencies); exit != 0 {
		t.Fatalf("run(update) = %d", exit)
	}
	if !called {
		t.Fatal("update dependency was not called")
	}
}

func TestRunUpdateRejectsArguments(t *testing.T) {
	t.Parallel()
	called := false
	stderr := new(bytes.Buffer)
	exit := run(context.Background(), []string{"update", "latest"}, io.Discard, stderr, commandDependencies{
		update: func(context.Context, string, io.Writer, io.Writer) error {
			called = true
			return nil
		},
	})
	if exit != 2 || called || !strings.Contains(stderr.String(), "arguments are not supported") {
		t.Fatalf("run(update latest) = %d, called = %t, stderr = %q", exit, called, stderr.String())
	}
}

func TestRunUpdatePreservesContainerExitCode(t *testing.T) {
	t.Parallel()
	exit := run(context.Background(), []string{"update"}, io.Discard, io.Discard, commandDependencies{
		update: func(context.Context, string, io.Writer, io.Writer) error {
			return claudeUpdateExitError{code: 23}
		},
	})
	if exit != 23 {
		t.Fatalf("run(update) = %d, want 23", exit)
	}
}

func TestRunForwardsUpdateAfterSeparatorToClaude(t *testing.T) {
	t.Parallel()
	recording := &clitest.RecordingLauncher{}
	exit := run(context.Background(), []string{"--", "update"}, io.Discard, io.Discard, commandDependencies{
		launch: claudeDependencies(recording),
		update: func(context.Context, string, io.Writer, io.Writer) error {
			t.Fatal("claude-safe -- update must not call the host updater")
			return nil
		},
	})
	if exit != 0 || recording.Command[len(recording.Command)-1] != "update" {
		t.Fatalf("run(-- update) = %d, command = %#v", exit, recording.Command)
	}
}

type claudeUpdateExitError struct{ code int }

func (err claudeUpdateExitError) Error() string { return "update failed" }
func (err claudeUpdateExitError) ExitCode() int { return err.code }

func claudeDependencies(productLauncher cli.Launcher) cli.Dependencies {
	return cli.Dependencies{
		Discover: func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		ResolveConfig: func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
			return cli.ResolvedConfig{
				Plan:            launchplan.Plan{},
				Image:           "image",
				ClaudeArguments: []string{"--dangerously-skip-permissions", "--model", "opus"},
			}, nil
		},
		NewLauncher: func(string) (cli.Launcher, error) { return productLauncher, nil },
	}
}
