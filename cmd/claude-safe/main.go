// Command claude-safe runs interactive Claude Code in the isolated project environment.
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

const defaultImage = "agents-safe-mvp:local"

const usage = `Usage: claude-safe update
       claude-safe [--project PATH] [--image REF] [--no-host-mcp] [--use-host-python-venv] [--force-exec] [-- CLAUDE ARG...]

Run interactive Claude Code for the current Git project inside the shared ephemeral Sysbox container.
Arguments after -- are forwarded to Claude Code; the launcher never runs another executable.

The update command updates the Linux Claude Code installation shared by managed containers.

  --no-host-mcp  Do not forward host MCP servers into the container. By default eligible loopback
                 servers from Codex and Claude host configuration use one confined relay.
  --use-host-python-venv Expose project-local host Python virtual environments instead of masking them.
  --force-exec   Execute in the owned running container despite a creation fingerprint mismatch.
  --image        Explicitly select an image and bypass automatic .agents-safe/Dockerfile selection.`

const updateUsage = `Usage: claude-safe update

Update the Linux Claude Code installation in the Docker named volume shared by managed containers.
This command needs Docker and the default image, but does not need Git or Sysbox.`

type commandDependencies struct {
	launch cli.Dependencies
	update func(context.Context, string, io.Writer, io.Writer) error
}

func config() cli.Config {
	return cli.Config{
		Name:         "claude-safe",
		DefaultImage: defaultImage,
		Usage:        usage,
		BuildCommand: launcher.ClaudeCommand,
		SelectArguments: func(resolved cli.ResolvedConfig) []string {
			return resolved.ClaudeArguments
		},
		WarnWhenClaudeHomeAbsent: true,
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
		update: launcher.UpdateClaude,
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
		fmt.Fprintln(stderr, "claude-safe update: arguments are not supported")
		fmt.Fprintln(stderr, updateUsage)
		return 2
	}
	if update == nil {
		fmt.Fprintln(stderr, "claude-safe update: updater dependency is not configured")
		return 1
	}
	if err := update(ctx, defaultImage, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "claude-safe update: %v\n", err)
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
