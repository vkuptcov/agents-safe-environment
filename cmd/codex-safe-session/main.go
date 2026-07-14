package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	containerRuntime "github.com/vkuptcov/agents-safe-environment/internal/container"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

type application struct {
	serve func(context.Context, *log.Logger) error
	run   func(context.Context, session.RunnerConfig) error
}

func main() {
	os.Exit(runCLI(
		context.Background(),
		os.Args[1:],
		os.Stdin,
		os.Stdout,
		os.Stderr,
		application{serve: serveManager, run: session.RunCommand},
	))
}

func runCLI(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	app application,
) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "codex-safe-session: subcommand is required")
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "codex-safe-session: help accepts no arguments")
			return 2
		}
		printUsage(stdout)
		return 0
	case "serve":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "codex-safe-session: serve accepts no arguments")
			return 2
		}
		serveContext, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := app.serve(serveContext, log.New(stderr, "codex-safe-session: ", 0)); err != nil {
			fmt.Fprintf(stderr, "codex-safe-session: %v\n", err)
			return 1
		}
		return 0
	case "run":
		if len(args) < 3 || args[1] != "--" {
			fmt.Fprintln(stderr, "codex-safe-session: run requires -- COMMAND [ARG...]")
			return 2
		}
		err := app.run(ctx, session.RunnerConfig{
			SocketPath:     session.DefaultSocketPath,
			StartupTimeout: session.DefaultStartupTimeout,
			Command:        args[2:],
			Stdin:          stdin,
			Stdout:         stdout,
			Stderr:         stderr,
		})
		if err == nil {
			return 0
		}
		var exitError interface{ ExitCode() int }
		if errors.As(err, &exitError) {
			var diagnostic interface{ WrapperDiagnostic() bool }
			if errors.As(err, &diagnostic) && diagnostic.WrapperDiagnostic() {
				fmt.Fprintf(stderr, "codex-safe-session: %v\n", err)
			}
			return exitError.ExitCode()
		}
		fmt.Fprintf(stderr, "codex-safe-session: %v\n", err)
		return 1
	default:
		fmt.Fprintf(stderr, "codex-safe-session: unknown subcommand %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func serveManager(ctx context.Context, logger *log.Logger) error {
	supervisor, err := containerRuntime.NewSupervisorFromEnvironment(logger)
	if err != nil {
		return err
	}
	return supervisor.Serve(ctx)
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  codex-safe-session serve")
	fmt.Fprintln(output, "  codex-safe-session run -- COMMAND [ARG...]")
}
