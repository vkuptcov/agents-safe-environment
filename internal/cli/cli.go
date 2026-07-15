// Package cli holds the shared launcher command scaffold. codex-safe and agents-safe differ only in
// their name, usage text, Codex-home policy, and how post-flag arguments become the container
// command; everything else (flag parsing, project discovery, plan building, exit-code propagation)
// lives here so the two binaries cannot drift.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

// Docker launches a plan's command in the managed project container.
type Docker interface {
	Launch(ctx context.Context, plan launcher.Plan, image string, command []string) error
}

// App carries the collaborators a launcher CLI needs. Production wires the real implementations;
// tests substitute fakes.
type App struct {
	Discover  func(context.Context, string) (gitproject.Project, error)
	BuildPlan func(gitproject.Project) (launcher.Plan, error)
	Docker    Docker
}

// Config describes one launcher binary's identity and command policy.
type Config struct {
	// Name is the program name used in the flag set and diagnostics.
	Name string
	// DefaultImage is the outer-container image used when --image is not given.
	DefaultImage string
	// Usage is the full help block printed for --help and before usage errors.
	Usage string
	// BuildCommand turns the post-flag arguments into the container command. A returned error is a
	// usage error (exit code 2): agents-safe uses it to reject an empty command; codex-safe never
	// errors because it always wraps the image-owned Codex binary.
	BuildCommand func(args []string) ([]string, error)
}

// Run parses args, builds the command, discovers the project, builds the plan, and launches the
// command, returning the process exit code.
func Run(ctx context.Context, cfg Config, args []string, stdout, stderr io.Writer, app App) int {
	flags := flag.NewFlagSet(cfg.Name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	projectPath := flags.String("project", ".", "Git project path")
	image := flags.String("image", cfg.DefaultImage, "outer container image")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, cfg.Usage)
			return 0
		}
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		fmt.Fprintln(stderr, cfg.Usage)
		return 2
	}

	command, err := cfg.BuildCommand(flags.Args())
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		fmt.Fprintln(stderr, cfg.Usage)
		return 2
	}

	project, err := app.Discover(ctx, *projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		return 1
	}
	plan, err := app.BuildPlan(project)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		return 1
	}
	if err := app.Docker.Launch(ctx, plan, *image, command); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		return errorExitCode(err)
	}
	return 0
}

// errorExitCode returns the launch error's own exit code when it carries one, so a failed command's
// status propagates through the launcher.
func errorExitCode(err error) int {
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) {
		if code := exitError.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}
