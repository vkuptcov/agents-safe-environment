// Command agents-safe runs a requested command in the isolated project environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const defaultImage = "codex-safe-mvp:local"

const usage = `Usage: agents-safe init [--project PATH]
       agents-safe [--project PATH] [--image REF] [--no-host-mcp] [--] COMMAND [ARG...]

Run a command for the current Git project inside an ephemeral Sysbox container.
Options must appear before COMMAND; COMMAND is executed directly without a shell.

The init command creates local .agents-safe templates and updates the root .gitignore.
Use agents-safe -- init to execute a container command named init.

  --no-host-mcp  Do not forward host MCP servers into the container. By default a loopback
                 MCP server in the base config.toml is reached through a confined relay.
  --image        Explicitly select an image and bypass automatic .agents-safe/Dockerfile selection.`

const initUsage = `Usage: agents-safe init [--project PATH]

Create local .agents-safe/Dockerfile.sample and .agents-safe/config.toml files at the selected
Git worktree root and add exact rules for them to the root .gitignore.`

type commandDependencies struct {
	discover        func(context.Context, string) (gitproject.Project, error)
	buildLaunchPlan func(gitproject.Project) (launchplan.Plan, error)
	initialize      func(string) (string, error)
	newLauncher     func() (cli.Launcher, error)
}

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
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, productionDependencies()))
}

func productionDependencies() commandDependencies {
	return commandDependencies{
		discover:        gitproject.Discover,
		buildLaunchPlan: launchplan.Build,
		initialize:      projectenv.Initialize,
		newLauncher: func() (cli.Launcher, error) {
			return launcher.NewDockerLauncher(launcher.CodexHomeOptional)
		},
	}
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies commandDependencies,
) int {
	if len(args) > 0 && args[0] == "init" {
		return runInit(ctx, args[1:], stdout, stderr, dependencies)
	}

	docker, err := dependencies.newLauncher()
	if err != nil {
		fmt.Fprintf(stderr, "agents-safe: initialize Docker launcher: %v\n", err)
		return 1
	}
	return cli.Run(
		ctx,
		config(),
		args,
		stdout,
		stderr,
		cli.Dependencies{
			Discover:        dependencies.discover,
			BuildLaunchPlan: dependencies.buildLaunchPlan,
			Launcher:        docker,
		},
	)
}

func runInit(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies commandDependencies,
) int {
	flags := pflag.NewFlagSet("agents-safe init", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.SetInterspersed(false)
	projectPath := flags.String("project", ".", "Git project path")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fmt.Fprintln(stdout, initUsage)
			return 0
		}
		fmt.Fprintf(stderr, "agents-safe init: %v\n", err)
		fmt.Fprintln(stderr, initUsage)
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "agents-safe init: positional arguments are not supported")
		fmt.Fprintln(stderr, initUsage)
		return 2
	}

	project, err := dependencies.discover(ctx, *projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "agents-safe init: %v\n", err)
		return 1
	}
	contextPath, err := dependencies.initialize(project.WorktreeRoot)
	if err != nil {
		fmt.Fprintf(stderr, "agents-safe init: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Initialized %s\n", contextPath)
	return 0
}
