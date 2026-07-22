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
	"github.com/vkuptcov/agents-safe-environment/internal/launchcli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const defaultImage = "codex-safe-mvp:local"

const usage = `Usage: agents-safe init [--project PATH]
       agents-safe [--project PATH] [--image REF] [--no-host-mcp] [--] COMMAND [ARG...]

Run a command for the current Git project inside an ephemeral Sysbox container.
Options must appear before COMMAND; COMMAND is executed directly without a shell.

The init command creates local .agents-safe templates without modifying the root .gitignore.
Use agents-safe -- init to execute a container command named init.

  --no-host-mcp  Do not forward host MCP servers into the container. By default eligible loopback
                 servers from Codex and Claude host configuration use one confined relay.
  --image        Explicitly select an image and bypass automatic .agents-safe/Dockerfile selection.`

const initUsage = `Usage: agents-safe init [--project PATH] [--host-caches=auto|none|go_build,go_modules,uv]

Create local .agents-safe/Dockerfile.sample, .agents-safe/config.toml, and .agents-safe/.gitignore files at the
selected Git worktree root. --host-caches defaults to auto and snapshots existing host Go and uv caches only for a
newly created config.toml. Initialization does not construct a Docker launcher or modify the root .gitignore.`

type initializationResult struct {
	Path        string
	Created     bool
	Caches      []projectenv.DependencyCacheConfig
	Diagnostics []string
}

type commandDependencies struct {
	discover      func(context.Context, string) (gitproject.Project, error)
	resolveConfig func(gitproject.Project, string, launchplan.Overrides) (cli.ResolvedConfig, error)
	initialize    func(context.Context, gitproject.Project, launchcli.HostCacheSelection) (initializationResult, error)
	newLauncher   func(string) (cli.Launcher, error)
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
		ValidateInvocation: func(args []string) error {
			if len(args) == 0 {
				return errors.New("command is required")
			}
			return nil
		},
		BuildCommand: func(_ []string, args []string) []string { return args },
	}
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, productionDependencies()))
}

func productionDependencies() commandDependencies {
	return commandDependencies{
		discover:      gitproject.Discover,
		resolveConfig: launchcli.ResolveConfig,
		initialize: func(ctx context.Context, project gitproject.Project, selection launchcli.HostCacheSelection) (initializationResult, error) {
			result := initializationResult{}
			path, created, err := projectenv.InitializeLazy(project.WorktreeRoot, func() (projectenv.ProjectConfig, error) {
				host, err := launcher.ResolveHostEnvironment()
				if err != nil {
					return projectenv.ProjectConfig{}, err
				}
				defaults, err := launcher.DefaultProjectConfig(project, host, defaultImage)
				if err != nil {
					return projectenv.ProjectConfig{}, err
				}
				caches, err := launchcli.ResolveHostCaches(ctx, selection, project, defaults, host.HomeDir)
				if err != nil {
					return projectenv.ProjectConfig{}, err
				}
				defaults.Common.DependencyCaches = caches.Caches
				result.Caches = caches.Caches
				result.Diagnostics = caches.Diagnostics
				return defaults, nil
			})
			if err != nil {
				return initializationResult{}, err
			}
			result.Path = path
			result.Created = created
			return result, nil
		},
		newLauncher: func(hostHome string) (cli.Launcher, error) {
			return launcher.NewDockerLauncher(hostHome)
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

	return cli.Run(
		ctx,
		config(),
		args,
		stdout,
		stderr,
		cli.Dependencies{
			Discover:      dependencies.discover,
			ResolveConfig: dependencies.resolveConfig,
			NewLauncher:   dependencies.newLauncher,
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
	hostCaches := flags.String("host-caches", "auto", "Host caches: auto, none, or go_build,go_modules,uv")
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
	selection, err := launchcli.ParseHostCacheSelection(*hostCaches)
	if err != nil {
		fmt.Fprintf(stderr, "agents-safe init: %v\n", err)
		return 2
	}

	project, err := dependencies.discover(ctx, *projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "agents-safe init: %v\n", err)
		return 1
	}
	result, err := dependencies.initialize(ctx, project, selection)
	if err != nil {
		fmt.Fprintf(stderr, "agents-safe init: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Initialized %s\n", result.Path)
	if !result.Created {
		fmt.Fprintln(stdout, "Existing config.toml preserved; host cache snapshot unchanged.")
		return 0
	}
	if len(result.Caches) == 0 && selection.Auto {
		fmt.Fprintln(stdout, "No existing shared read-write host dependency caches detected")
	}
	for _, diagnostic := range result.Diagnostics {
		fmt.Fprintf(stderr, "agents-safe init: %s\n", diagnostic)
	}
	for _, cache := range result.Caches {
		fmt.Fprintf(stdout, "Host dependency cache: %s  %s  shared_rw\n", cache.Kind, cache.Source)
	}
	return 0
}
