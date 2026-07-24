// Package launchcli composes host and project resolution into the launch-facing configuration the
// public binaries hand to cli.Run. It sits above both launcher and cli so neither leaf package has to
// depend on the other, and it is the single adapter all launchers share so they cannot silently drift
// when the resolved configuration shape changes.
package launchcli

import (
	"context"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// ResolveConfig resolves the host environment and project configuration for one discovered project and
// flattens the result into the view cli.Run consumes.
func ResolveConfig(
	project gitproject.Project,
	defaultImage string,
	overrides launchplan.Overrides,
) (cli.ResolvedConfig, error) {
	host, err := launcher.ResolveHostEnvironment()
	if err != nil {
		return cli.ResolvedConfig{}, err
	}
	// A config-less launch generates the default in memory; warnings from that discovery (for example a uv
	// config override skipped for safety) are captured here and surfaced before launch. Routine "cache
	// unavailable" diagnostics are intentionally not shown at launch — they are init-time noise.
	var warnings []string
	resolved, err := launcher.ResolveProjectConfig(project, host, defaultImage, overrides,
		func(project gitproject.Project, host launcher.HostEnvironment) (projectenv.ProjectConfig, error) {
			config, resolution, err := GenerateDefaultConfig(
				context.Background(), project, host, defaultImage, autoHostCacheSelection())
			if err != nil {
				return projectenv.ProjectConfig{}, err
			}
			warnings = resolution.Warnings
			return config, nil
		})
	if err != nil {
		return cli.ResolvedConfig{}, err
	}
	return cli.ResolvedConfig{
		Plan:                 resolved.Resolution.Plan,
		Image:                resolved.Config.Common.Image,
		Options:              resolved.Options,
		CodexArguments:       resolved.Config.Codex.Arguments,
		ClaudeArguments:      resolved.Config.Claude.Arguments,
		Degradations:         resolved.Resolution.Degradations,
		Warnings:             warnings,
		DefaultCodexHomeSet:  resolved.DefaultCodexHomeSet,
		DefaultClaudeHomeSet: resolved.DefaultClaudeHomeSet,
		HostHome:             host.HomeDir,
	}, nil
}

// GenerateDefaultConfig builds the complete default project configuration for a project that has no
// persisted config.toml, seeded with the host dependency caches for selection. It is the single generator
// shared by `agents-safe init` (which encodes the result to config.toml) and a config-less launch (which
// keeps it in memory), so launching without config.toml resolves the same contract init would have
// written. The returned HostCacheResolution carries the non-fatal auto-discovery diagnostics. The cache
// probes impose their own timeouts, so context.Background() is sufficient and cannot hang a launch.
func GenerateDefaultConfig(
	ctx context.Context,
	project gitproject.Project,
	host launcher.HostEnvironment,
	image string,
	selection HostCacheSelection,
) (projectenv.ProjectConfig, HostCacheResolution, error) {
	config, err := launcher.DefaultProjectConfig(project, host, image)
	if err != nil {
		return projectenv.ProjectConfig{}, HostCacheResolution{}, err
	}
	resolution, err := ResolveHostCaches(ctx, selection, project, config, host.HomeDir)
	if err != nil {
		return projectenv.ProjectConfig{}, HostCacheResolution{}, err
	}
	config.Common.DependencyCaches = resolution.Caches
	return config, resolution, nil
}

// autoHostCacheSelection returns the init default cache selection: auto-discover Go and uv caches,
// dropping any that are unavailable. "auto" is a fixed literal that ParseHostCacheSelection never rejects.
func autoHostCacheSelection() HostCacheSelection {
	selection, _ := ParseHostCacheSelection("auto")
	return selection
}
