// Package cli holds the shared launcher command scaffold. Product and generic launchers differ only
// in their name, usage text, state warning policy, and how post-flag arguments become the container
// command; everything else (flag parsing, project discovery, plan building, exit-code propagation)
// lives here so the binaries cannot drift.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/pflag"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

// Launcher runs a command in the managed container described by a launch plan.
type Launcher interface {
	Launch(ctx context.Context, plan launchplan.Plan, image string, command []string, options launchplan.Options) error
}

// Dependencies contains the project discovery and container-launching operations used by Run.
// Product binaries wire production implementations; tests substitute focused doubles.
type Dependencies struct {
	Discover      func(context.Context, string) (gitproject.Project, error)
	ResolveConfig func(gitproject.Project, string, launchplan.Overrides) (ResolvedConfig, error)
	// NewLauncher is deliberately lazy: usage validation and project configuration must complete
	// before host identity or Docker-facing construction can fail.
	NewLauncher func(hostHome string) (Launcher, error)
}

// ResolvedConfig is the resolver output shared by command assembly and container launch.
type ResolvedConfig struct {
	Plan            launchplan.Plan
	Image           string
	Options         launchplan.Options
	CodexArguments  []string
	ClaudeArguments []string
	Degradations    []launchplan.Degradation
	// Warnings are operator-facing notices surfaced before launch (for example a uv config override that
	// automatic discovery skipped for safety). They are distinct from role Degradations.
	Warnings             []string
	DefaultCodexHomeSet  bool
	DefaultClaudeHomeSet bool
	HostHome             string
}

// Config describes one launcher binary's identity and command policy.
type Config struct {
	// Name is the program name used in the flag set and diagnostics.
	Name string
	// DefaultImage is the container image used when --image is not given.
	DefaultImage string
	// Usage is the full help block printed for --help and before usage errors.
	Usage string
	// ValidateInvocation rejects config-independent command errors before project discovery. agents-safe uses it to
	// reject a missing command; codex-safe accepts an empty invocation for interactive use.
	ValidateInvocation func(args []string) error
	// BuildCommand combines a product's configured arguments and post-flag invocation arguments. It runs only after
	// project resolution because agents-safe must preserve its pre-discovery missing-command usage error.
	BuildCommand func(configuredArgs, invocationArgs []string) []string
	// SelectArguments chooses this product's configured argv from the shared resolved project config.
	// It is nil for agents-safe, whose invocation is already the complete command.
	SelectArguments func(ResolvedConfig) []string
	// WarnWhenCodexHomeAbsent selects codex-safe's warning when no default Codex home exists on this host.
	WarnWhenCodexHomeAbsent bool
	// WarnWhenClaudeHomeAbsent selects claude-safe's warning when no complete default Claude state exists.
	WarnWhenClaudeHomeAbsent bool
}

// Run parses args, validates config-independent usage, resolves project config, builds the command, and launches it.
func Run(ctx context.Context, cfg Config, args []string, stdout, stderr io.Writer, dependencies Dependencies) int {
	flags := pflag.NewFlagSet(cfg.Name, pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.SetInterspersed(false)
	projectPath := flags.String("project", ".", "Git project path")
	image := flags.String("image", cfg.DefaultImage, "container image")
	noHostMCP := flags.Bool("no-host-mcp", false,
		"disable host MCP forwarding: no product config read, no forwarders, no relay, no mount")
	useHostPythonVenv := flags.Bool("use-host-python-venv", false,
		"use project-local host Python virtual environments instead of masking them")
	keepContainer := flags.Bool("keep-container", false,
		"keep the session container after it stops so a later launch restarts it instead of creating a new one")
	forceExec := flags.Bool("force-exec", false,
		"execute in an owned running container even when its creation fingerprint differs")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fmt.Fprintln(stdout, cfg.Usage)
			return 0
		}
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		fmt.Fprintln(stderr, cfg.Usage)
		return 2
	}

	invocation := flags.Args()
	if cfg.ValidateInvocation != nil {
		if err := cfg.ValidateInvocation(invocation); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
			fmt.Fprintln(stderr, cfg.Usage)
			return 2
		}
	}

	project, err := dependencies.Discover(ctx, *projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		return 1
	}
	overrides := launchplan.Overrides{
		Image:                     *image,
		ImageOverride:             flags.Changed("image"),
		NoHostMCP:                 *noHostMCP,
		NoHostMCPOverride:         flags.Changed("no-host-mcp"),
		UseHostPythonVenv:         *useHostPythonVenv,
		UseHostPythonVenvOverride: flags.Changed("use-host-python-venv"),
		KeepContainer:             *keepContainer,
		KeepContainerOverride:     flags.Changed("keep-container"),
	}
	resolved, err := dependencies.ResolveConfig(project, cfg.DefaultImage, overrides)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		return 1
	}
	// force-exec is invocation-only adoption policy. It is deliberately applied after project
	// resolution so it cannot become persistent config or participate in the creation fingerprint.
	resolved.Options.ForceExec = *forceExec
	printDegradationWarnings(cfg, resolved, stderr)
	command := invocation
	if cfg.BuildCommand != nil {
		configured := resolved.CodexArguments
		if cfg.SelectArguments != nil {
			configured = cfg.SelectArguments(resolved)
		}
		command = cfg.BuildCommand(configured, invocation)
	}
	if dependencies.NewLauncher == nil {
		fmt.Fprintf(stderr, "%s: launcher dependency is not configured\n", cfg.Name)
		return 1
	}
	launcher, err := dependencies.NewLauncher(resolved.HostHome)
	if err != nil {
		fmt.Fprintf(stderr, "%s: initialize Docker launcher: %v\n", cfg.Name, err)
		return 1
	}
	if launcher == nil {
		fmt.Fprintf(stderr, "%s: launcher dependency is not configured\n", cfg.Name)
		return 1
	}
	if err := launcher.Launch(ctx, resolved.Plan, resolved.Image, command, resolved.Options); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cfg.Name, err)
		return errorExitCode(err)
	}
	return 0
}

func printDegradationWarnings(cfg Config, resolved ResolvedConfig, stderr io.Writer) {
	degradations := resolved.Degradations
	if cfg.WarnWhenCodexHomeAbsent && !resolved.DefaultCodexHomeSet {
		degradations = launchplan.AppendCodexHomeAbsentDegradation(degradations)
	}
	if cfg.WarnWhenClaudeHomeAbsent && !resolved.DefaultClaudeHomeSet {
		degradations = launchplan.AppendClaudeHomeAbsentDegradation(degradations)
	}
	for _, degradation := range degradations {
		fmt.Fprintf(stderr, "%s: warning: %s\n", cfg.Name, launchplan.DegradationMessage(degradation.Role))
	}
	for _, warning := range resolved.Warnings {
		fmt.Fprintf(stderr, "%s: warning: %s\n", cfg.Name, warning)
	}
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
