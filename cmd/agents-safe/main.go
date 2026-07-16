// Command agents-safe runs a requested command in the isolated project environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

const defaultImage = "codex-safe-mvp:local"

const usage = `Usage: agents-safe [--project PATH] [--image REF] [--] COMMAND [ARG...]

Run a command for the current Git project inside an ephemeral Sysbox container.
Options must appear before COMMAND; COMMAND is executed directly without a shell.`

// config returns the agents-safe launcher configuration. It is a function so tests can drive the
// same command policy the binary uses.
func config() cli.Config {
	return cli.Config{
		Name:         "agents-safe",
		DefaultImage: defaultImage,
		Usage:        usage,
		// agents-safe requires a command; every argument stays a separate argv element and runs
		// directly through the session wrapper without a shell.
		BuildCommand: func(args []string) ([]string, error) {
			if len(args) == 0 {
				return nil, errors.New("command is required")
			}
			return args, nil
		},
	}
}

func main() {
	docker, err := launcher.NewDockerLauncher(launcher.CodexHomeOptional)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents-safe: initialize Docker launcher: %v\n", err)
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
