// Command agents-safe runs a requested command in the isolated project environment.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

const defaultImage = "codex-safe-mvp:local"

type dockerLauncher interface {
	Launch(ctx context.Context, plan launcher.Plan, image string, command []string) error
}

type application struct {
	discover  func(context.Context, string) (gitproject.Project, error)
	buildPlan func(gitproject.Project) (launcher.Plan, error)
	docker    dockerLauncher
}

func main() {
	docker, err := launcher.NewDocker()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents-safe: initialize Docker launcher: %v\n", err)
		os.Exit(1)
	}
	os.Exit(run(
		context.Background(),
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		application{
			discover:  gitproject.Discover,
			buildPlan: launcher.BuildPlan,
			docker:    docker,
		},
	))
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	app application,
) int {
	flags := flag.NewFlagSet("agents-safe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	projectPath := flags.String("project", ".", "Git project path")
	image := flags.String("image", defaultImage, "outer container image")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "agents-safe: %v\n", err)
		printUsage(stderr)
		return 2
	}

	command := flags.Args()
	if len(command) == 0 {
		fmt.Fprintln(stderr, "agents-safe: command is required")
		printUsage(stderr)
		return 2
	}

	project, err := app.discover(ctx, *projectPath)
	if err != nil {
		return reportError(stderr, err)
	}
	plan, err := app.buildPlan(project)
	if err != nil {
		return reportError(stderr, err)
	}
	if err := app.docker.Launch(ctx, plan, *image, command); err != nil {
		fmt.Fprintf(stderr, "agents-safe: %v\n", err)
		return errorExitCode(err)
	}
	return 0
}

func reportError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "agents-safe: %v\n", err)
	return 1
}

func errorExitCode(err error) int {
	var exitError interface {
		ExitCode() int
	}
	if errors.As(err, &exitError) {
		if code := exitError.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: agents-safe [--project PATH] [--image REF] [--] COMMAND [ARG...]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Run a command for the current Git project inside an ephemeral Sysbox container.")
	fmt.Fprintln(output, "Options must appear before COMMAND; COMMAND is executed directly without a shell.")
}
