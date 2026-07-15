//go:build smoke

// Command codex-safe-probe is a test-only transport. It drives the create, reuse, and exec path of
// the launcher with an arbitrary command through codex-safe-session run, exactly as the product CLI
// did before it stopped accepting arbitrary commands. It exists only in builds tagged "smoke" so the
// product codex-safe binary never carries an arbitrary-command execution surface.
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
		fmt.Fprintf(os.Stderr, "codex-safe-probe: initialize Docker launcher: %v\n", err)
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
	flags := flag.NewFlagSet("codex-safe-probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	projectPath := flags.String("project", ".", "Git project path")
	image := flags.String("image", defaultImage, "outer container image")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "codex-safe-probe: %v\n", err)
		printUsage(stderr)
		return 2
	}

	command := flags.Args()
	if len(command) == 0 {
		fmt.Fprintln(stderr, "codex-safe-probe: probe command is required after --")
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
		fmt.Fprintf(stderr, "codex-safe-probe: %v\n", err)
		return errorExitCode(err)
	}
	return 0
}

func reportError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "codex-safe-probe: %v\n", err)
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
	fmt.Fprintln(output, "Usage: codex-safe-probe [--project PATH] [--image REF] -- COMMAND [ARG...]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Test-only transport that runs an arbitrary probe command inside a Sysbox session.")
}
