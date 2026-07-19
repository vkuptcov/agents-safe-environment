// Package launchcli composes host and project resolution into the launch-facing configuration the
// public binaries hand to cli.Run. It sits above both launcher and cli so neither leaf package has to
// depend on the other, and it is the single adapter both commands share so they cannot silently drift
// when the resolved configuration shape changes.
package launchcli

import (
	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
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
	resolved, err := launcher.ResolveProjectConfig(project, host, defaultImage, overrides)
	if err != nil {
		return cli.ResolvedConfig{}, err
	}
	return cli.ResolvedConfig{
		Plan:                resolved.Resolution.Plan,
		Image:               resolved.Config.Common.Image,
		Options:             resolved.Options,
		CodexArguments:      resolved.Config.Codex.Arguments,
		Degradations:        resolved.Resolution.Degradations,
		DefaultCodexHomeSet: resolved.DefaultCodexHomeSet,
		HostHome:            host.HomeDir,
	}, nil
}
