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
	resolved, err := launcher.ResolveProjectConfig(project, host, defaultImage, overrides, discoverDefaultCaches)
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
		DefaultCodexHomeSet:  resolved.DefaultCodexHomeSet,
		DefaultClaudeHomeSet: resolved.DefaultClaudeHomeSet,
		HostHome:             host.HomeDir,
	}, nil
}

// discoverDefaultCaches seeds a config-less launch with the same caches `agents-safe init` would
// auto-discover, so a project whose config.toml was never generated (for example a linked worktree, where
// the git-ignored file is not carried over) still mounts its host dependency caches. It uses the init
// default `auto` selection, which drops unavailable caches non-fatally. The probes impose their own
// timeouts, so context.Background() is sufficient and cannot hang the launch.
func discoverDefaultCaches(
	project gitproject.Project,
	host launcher.HostEnvironment,
	defaults projectenv.ProjectConfig,
) ([]projectenv.DependencyCacheConfig, error) {
	selection, err := ParseHostCacheSelection("auto")
	if err != nil {
		return nil, err
	}
	resolution, err := ResolveHostCaches(context.Background(), selection, project, defaults, host.HomeDir)
	if err != nil {
		return nil, err
	}
	return resolution.Caches, nil
}
