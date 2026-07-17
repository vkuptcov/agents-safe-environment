package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	containerRuntime "github.com/vkuptcov/agents-safe-environment/internal/container"
	"github.com/vkuptcov/agents-safe-environment/internal/relay"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

type application struct {
	serve func(context.Context, *log.Logger) error
	run   func(context.Context, session.CommandConfig) error
	relay func(context.Context, relay.Config) error
}

func main() {
	os.Exit(runCLI(
		context.Background(),
		os.Args[1:],
		os.Stdin,
		os.Stdout,
		os.Stderr,
		application{serve: serveManager, run: session.RunCommand, relay: relay.Run},
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
	case "relay":
		// The relay sidecar's whole lifetime is this call. It is stopped by a signal or by its own
		// lease closing, so it shares serve's signal handling and nothing else.
		relayContext, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		config, err := parseRelayFlags(args[1:], stderr)
		if err != nil {
			return 2
		}
		config.Log = log.New(stderr, "codex-safe-session: ", 0)
		if err := app.relay(relayContext, config); err != nil {
			fmt.Fprintf(stderr, "codex-safe-session: %v\n", err)
			return 1
		}
		return 0
	case "run":
		if len(args) < 3 || args[1] != "--" {
			fmt.Fprintln(stderr, "codex-safe-session: run requires -- COMMAND [ARG...]")
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

// endpointList collects a repeatable --endpoint flag. Its order is the launch's sorted endpoint
// order, which is what fixes each endpoint's socket index on both sides of the mount.
type endpointList []string

func (list *endpointList) String() string { return strings.Join(*list, ",") }

func (list *endpointList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("endpoint must not be empty")
	}
	*list = append(*list, value)
	return nil
}

func parseRelayFlags(args []string, stderr io.Writer) (relay.Config, error) {
	flags := flag.NewFlagSet("relay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	generation := flags.String("generation", "", "generation directory as seen inside this container")
	initialLease := flags.Duration("initial-lease-timeout", 0, "bound on the wait for the session's first lease")
	var endpoints endpointList
	flags.Var(&endpoints, "endpoint", "host endpoint as host:port, repeated in the launch's sorted order")
	if err := flags.Parse(args); err != nil {
		return relay.Config{}, err
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "codex-safe-session: relay accepts no positional arguments")
		return relay.Config{}, errors.New("unexpected arguments")
	}
	return relay.Config{
		Generation:          *generation,
		Endpoints:           endpoints,
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
	fmt.Fprintln(output, "  codex-safe-session serve")
	fmt.Fprintln(output, "  codex-safe-session run -- COMMAND [ARG...]")
	fmt.Fprintln(output, "  codex-safe-session relay --generation DIR --endpoint HOST:PORT [--endpoint ...]")
}
