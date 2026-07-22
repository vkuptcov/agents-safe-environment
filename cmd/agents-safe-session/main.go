package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/pflag"

	containerRuntime "github.com/vkuptcov/agents-safe-environment/internal/container"
	"github.com/vkuptcov/agents-safe-environment/internal/relay"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

type application struct {
	serve     func(context.Context, *log.Logger) error
	run       func(context.Context, session.CommandConfig) error
	waitReady func(context.Context, string) error
	relay     func(context.Context, relay.Config) error
}

func main() {
	os.Exit(runCLI(
		context.Background(),
		os.Args[1:],
		os.Stdin,
		os.Stdout,
		os.Stderr,
		application{serve: serveManager, run: session.RunCommand, waitReady: session.WaitForSocket, relay: relay.Run},
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
		fmt.Fprintln(stderr, "agents-safe-session: subcommand is required")
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "agents-safe-session: help accepts no arguments")
			return 2
		}
		printUsage(stdout)
		return 0
	case "serve":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "agents-safe-session: serve accepts no arguments")
			return 2
		}
		serveContext, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := app.serve(serveContext, log.New(stderr, "agents-safe-session: ", 0)); err != nil {
			fmt.Fprintf(stderr, "agents-safe-session: %v\n", err)
			return 1
		}
		return 0
	case "relay":
		// The relay sidecar's whole lifetime is this call. It is stopped by a signal or by its own
		// lease closing, so it shares serve's signal handling and nothing else.
		relayContext, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		config, err := parseRelayFlags(args[1:], stderr)
		if err != nil {
			return 2
		}
		config.Log = log.New(stderr, "agents-safe-session: ", 0)
		if err := app.relay(relayContext, config); err != nil {
			fmt.Fprintf(stderr, "agents-safe-session: %v\n", err)
			return 1
		}
		return 0
	case "run":
		if len(args) < 3 || args[1] != "--" {
			fmt.Fprintln(stderr, "agents-safe-session: run requires -- COMMAND [ARG...]")
			return 2
		}
		err := app.run(ctx, session.CommandConfig{
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
				fmt.Fprintf(stderr, "agents-safe-session: %v\n", err)
			}
			return exitError.ExitCode()
		}
		fmt.Fprintf(stderr, "agents-safe-session: %v\n", err)
		return 1
	case "wait-ready":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "agents-safe-session: wait-ready accepts no arguments")
			return 2
		}
		if err := app.waitReady(ctx, session.DefaultSocketPath); err != nil {
			fmt.Fprintf(stderr, "agents-safe-session: wait for session readiness: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "agents-safe-session: unknown subcommand %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func parseRelayFlags(args []string, stderr io.Writer) (relay.Config, error) {
	flags := pflag.NewFlagSet("relay", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.SetInterspersed(false)
	generation := flags.String("generation", "", "generation directory as seen inside this container")
	initialLease := flags.Duration("initial-lease-timeout", 0, "bound on the wait for the session's first lease")
	endpoints := flags.StringArray("endpoint", nil, "host endpoint as host:port, repeated in the launch's sorted order")
	if err := flags.Parse(args); err != nil {
		return relay.Config{}, err
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "agents-safe-session: relay accepts no positional arguments")
		return relay.Config{}, errors.New("unexpected arguments")
	}
	for _, endpoint := range *endpoints {
		if strings.TrimSpace(endpoint) == "" {
			fmt.Fprintln(stderr, "agents-safe-session: endpoint must not be empty")
			return relay.Config{}, errors.New("endpoint must not be empty")
		}
	}
	return relay.Config{
		Generation:          *generation,
		Endpoints:           *endpoints,
		InitialLeaseTimeout: *initialLease,
	}, nil
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
	fmt.Fprintln(output, "  agents-safe-session serve")
	fmt.Fprintln(output, "  agents-safe-session wait-ready")
	fmt.Fprintln(output, "  agents-safe-session run -- COMMAND [ARG...]")
	fmt.Fprintln(output, "  agents-safe-session relay --generation DIR --endpoint HOST:PORT [--endpoint ...]")
}
