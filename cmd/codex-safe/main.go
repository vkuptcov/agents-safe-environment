// Command codex-safe runs interactive Codex in the isolated project environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launchcli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

const defaultImage = "codex-safe-mvp:local"

const usage = `Usage: codex-safe update
       codex-safe [--project PATH] [--image REF] [--no-host-mcp] [-- CODEX ARG...]

Run interactive Codex for the current Git project inside an ephemeral Sysbox container.
Arguments after -- are forwarded to Codex; the launcher never runs another executable.

The update command updates the Linux Codex installation shared by managed containers.

  --no-host-mcp  Do not forward host MCP servers into the container. By default a loopback
                 MCP server in the base config.toml is reached through a confined relay.
  --image        Explicitly select an image and bypass automatic .agents-safe/Dockerfile selection.`

const updateUsage = `Usage: codex-safe update

Update the Linux Codex installation in the Docker named volume shared by managed containers.
This command needs Docker and the default image, but does not need Git or Sysbox.`

type commandDependencies struct {
	launch cli.Dependencies
	update func(context.Context, string, io.Writer, io.Writer) error
}

// config returns the codex-safe launcher configuration. It is a function so tests can drive the
// same command policy the binary uses.
func config() cli.Config {
	return cli.Config{
		Name:         "codex-safe",
		DefaultImage: defaultImage,
		Usage:        usage,
		// The product always runs the image-owned Codex binary. Arguments after -- are Codex
		// arguments, never a standalone executable, so no arbitrary command reaches the container.
		BuildCommand:            launcher.CodexCommand,
		WarnWhenCodexHomeAbsent: true,
	}
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, productionDependencies()))
}

func productionDependencies() commandDependencies {
	return commandDependencies{
		launch: cli.Dependencies{
			Discover:      gitproject.Discover,
			ResolveConfig: launchcli.ResolveConfig,
			NewLauncher: func(hostHome string) (cli.Launcher, error) {
				return launcher.NewDockerLauncher(hostHome)
			},
		},
		update: launcher.UpdateCodex,
	}
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies commandDependencies,
) int {
	if len(args) > 0 && args[0] == "update" {
		return runUpdate(ctx, args[1:], stdout, stderr, dependencies.update)
	}
	return cli.Run(ctx, config(), args, stdout, stderr, dependencies.launch)
}

func runUpdate(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	update func(context.Context, string, io.Writer, io.Writer) error,
) int {
	for _, arg := range args {
		if len(args) == 1 && (arg == "--help" || arg == "-h") {
			fmt.Fprintln(stdout, updateUsage)
			return 0
		}
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "codex-safe update: arguments are not supported")
		fmt.Fprintln(stderr, updateUsage)
		return 2
	}
	if update == nil {
		fmt.Fprintln(stderr, "codex-safe update: updater dependency is not configured")
		return 1
	}
	if err := update(ctx, defaultImage, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "codex-safe update: %v\n", err)
		return commandExitCode(err)
	}
	return 0
}

func commandExitCode(err error) int {
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) && exitError.ExitCode() >= 0 {
		return exitError.ExitCode()
	}
	return 1
}
