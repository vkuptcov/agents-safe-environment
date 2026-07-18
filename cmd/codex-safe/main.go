// Command codex-safe runs interactive Codex in the isolated project environment.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

const defaultImage = "codex-safe-mvp:local"

const usage = `Usage: codex-safe [--project PATH] [--image REF] [--no-host-mcp] [-- CODEX ARG...]

Run interactive Codex for the current Git project inside an ephemeral Sysbox container.
Arguments after -- are forwarded to Codex; the launcher never runs another executable.

  --no-host-mcp  Do not forward host MCP servers into the container. By default a loopback
                 MCP server in the base config.toml is reached through a confined relay.
  --image        Explicitly select an image and bypass automatic .agents-safe/Dockerfile selection.`

// config returns the codex-safe launcher configuration. It is a function so tests can drive the
// same command policy the binary uses.
func config() cli.Config {
	return cli.Config{
		Name:         "codex-safe",
		DefaultImage: defaultImage,
		Usage:        usage,
		// The product always runs the image-owned Codex binary. Arguments after -- are Codex
		// arguments, never a standalone executable, so no arbitrary command reaches the container.
		BuildCommand: func(args []string) ([]string, error) {
			return launcher.DefaultCodexCommand(args), nil
		},
	}
}

func main() {
	docker, err := launcher.NewDockerLauncher(launcher.CodexHomeRequired)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex-safe: initialize Docker launcher: %v\n", err)
		os.Exit(1)
	}
	os.Exit(cli.Run(
		context.Background(),
		config(),
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		cli.Dependencies{Discover: gitproject.Discover, BuildLaunchPlan: launchplan.Build, Launcher: docker},
	))
}
