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

func TestConfigBuildsConfiguredCodexCommand(t *testing.T) {
	t.Parallel()
	recording := &clitest.RecordingLauncher{}
	deps := codexDependencies(recording)
	if exit := cli.Run(context.Background(), config(), []string{"--", "exec", "--sandbox", "read-only"},
		new(bytes.Buffer), new(bytes.Buffer), deps); exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	want := []string{launcher.CodexBinaryPath, "--model", "gpt-5", "exec", "--sandbox", "read-only"}
	if !reflect.DeepEqual(recording.Command, want) {
		t.Fatalf("command = %#v, want %#v", recording.Command, want)
	}
}

func TestConfigWarnsWhenHostCodexHomeIsAbsent(t *testing.T) {
	t.Parallel()
	stderr := new(bytes.Buffer)
	if exit := cli.Run(context.Background(), config(), nil, new(bytes.Buffer), stderr, codexDependencies(&clitest.RecordingLauncher{})); exit != 0 {
		t.Fatalf("Run() = %d", exit)
	}
	if !strings.Contains(stderr.String(), "using ephemeral state") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func codexDependencies(launcher cli.Launcher) cli.Dependencies {
	return cli.Dependencies{
		Discover: func(context.Context, string) (gitproject.Project, error) { return gitproject.Project{}, nil },
		ResolveConfig: func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error) {
			return cli.ResolvedConfig{
				Plan:           launchplan.Plan{},
				Image:          "image",
				CodexArguments: []string{"--sandbox", "danger-full-access", "--model", "gpt-5"},
			}, nil
		},
		NewLauncher: func(string) (cli.Launcher, error) { return launcher, nil },
	}
}
