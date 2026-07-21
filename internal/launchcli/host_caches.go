package launchcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launchcli/dependencies"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// HostCacheSelection is the parsed init-only cache choice.
type HostCacheSelection struct {
	Kinds []projectenv.DependencyCacheKind
	Auto  bool
}

// ParseHostCacheSelection accepts the supported init cache surface.
func ParseHostCacheSelection(value string) (HostCacheSelection, error) {
	if value == "" || value == "auto" {
		return HostCacheSelection{Auto: true, Kinds: append([]projectenv.DependencyCacheKind(nil), projectenv.DependencyCacheKindOrder...)}, nil
	}
	if value == "none" {
		return HostCacheSelection{}, nil
	}
	seen := map[projectenv.DependencyCacheKind]bool{}
	selection := HostCacheSelection{}
	for _, token := range strings.Split(value, ",") {
		kind := projectenv.DependencyCacheKind(token)
		if token == "" || !isSupportedHostCacheKind(kind) {
			return HostCacheSelection{}, fmt.Errorf("--host-caches accepts only auto, none, go_build, go_modules, and uv")
		}
		if seen[kind] {
			return HostCacheSelection{}, fmt.Errorf("--host-caches repeats %q", kind)
		}
		seen[kind] = true
	}
	for _, kind := range projectenv.DependencyCacheKindOrder {
		if seen[kind] {
			selection.Kinds = append(selection.Kinds, kind)
		}
	}
	return selection, nil
}

func isSupportedHostCacheKind(kind projectenv.DependencyCacheKind) bool {
	for _, supported := range projectenv.DependencyCacheKindOrder {
		if kind == supported {
			return true
		}
	}
	return false
}

// HostCacheResolution is the persisted snapshot and non-fatal auto-discovery diagnostics.
type HostCacheResolution struct {
	Caches      []projectenv.DependencyCacheConfig
	Diagnostics []string
}

// ResolveHostCaches resolves one init snapshot without Docker or network access.
func ResolveHostCaches(
	ctx context.Context,
	selection HostCacheSelection,
	project gitproject.Project,
	defaults projectenv.ProjectConfig,
	homeDir string,
) (HostCacheResolution, error) {
	return resolveHostCaches(ctx, selection, project.WorktreeRoot, homeDir, hostCacheResolutionInputs{
		resolveGo: dependencies.ResolveGoCaches,
		resolveUV: dependencies.ResolveUVCache,
		validate: func(caches []projectenv.DependencyCacheConfig) error {
			config := cloneConfigWithCaches(defaults, caches)
			_, err := launchplan.ResolveWithHostHome(project, defaults, config, homeDir)
			return err
		},
	})
}

type hostCacheResolutionInputs struct {
	resolveGo func(
		context.Context,
		[]projectenv.DependencyCacheKind,
		bool,
		string,
	) (dependencies.GoCacheResolution, error)
	resolveUV func(context.Context, bool, string, string) (dependencies.UVCacheResolution, error)
	validate  func([]projectenv.DependencyCacheConfig) error
}

func resolveHostCaches(
	ctx context.Context,
	selection HostCacheSelection,
	projectRoot string,
	homeDir string,
	inputs hostCacheResolutionInputs,
) (HostCacheResolution, error) {
	if inputs.resolveGo == nil || inputs.resolveUV == nil || inputs.validate == nil {
		return HostCacheResolution{}, fmt.Errorf("host cache resolution inputs are incomplete")
	}
	if err := inputs.validate(nil); err != nil {
		return HostCacheResolution{}, fmt.Errorf("validate project cache baseline: %w", err)
	}

	goKinds := make([]projectenv.DependencyCacheKind, 0, len(selection.Kinds))
	selectedUV := false
	for _, kind := range selection.Kinds {
		switch kind {
		case projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules:
			goKinds = append(goKinds, kind)
		case projectenv.DependencyCacheUV:
			selectedUV = true
		default:
			return HostCacheResolution{}, fmt.Errorf("unsupported dependency cache kind %q", kind)
		}
	}

	result := HostCacheResolution{}
	candidates := make([]projectenv.DependencyCacheConfig, 0, len(selection.Kinds))
	if len(goKinds) != 0 {
		goResult, err := inputs.resolveGo(ctx, goKinds, selection.Auto, homeDir)
		if err != nil {
			return HostCacheResolution{}, err
		}
		candidates = append(candidates, goResult.Caches...)
		result.Diagnostics = append(result.Diagnostics, goResult.Diagnostics...)
	}
	if selectedUV {
		uvResult, err := inputs.resolveUV(ctx, selection.Auto, homeDir, projectRoot)
		if err != nil {
			return HostCacheResolution{}, err
		}
		candidates = append(candidates, uvResult.Caches...)
		result.Diagnostics = append(result.Diagnostics, uvResult.Diagnostics...)
	}

	for _, kind := range projectenv.DependencyCacheKindOrder {
		for _, candidate := range candidates {
			if candidate.Kind != kind {
				continue
			}
			proposed := append(append([]projectenv.DependencyCacheConfig(nil), result.Caches...), candidate)
			if err := inputs.validate(proposed); err != nil {
				if !selection.Auto {
					return HostCacheResolution{}, fmt.Errorf("validate %s cache: %w", candidate.Kind, err)
				}
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("%s unavailable: %v", candidate.Kind, err))
				continue
			}
			result.Caches = proposed
		}
	}
	if !selection.Auto && len(selection.Kinds) == 0 {
		result.Caches = []projectenv.DependencyCacheConfig{}
	}
	return result, nil
}

func cloneConfigWithCaches(
	defaults projectenv.ProjectConfig,
	caches []projectenv.DependencyCacheConfig,
) projectenv.ProjectConfig {
	config := defaults
	config.Common.Mounts = append([]projectenv.MountConfig(nil), defaults.Common.Mounts...)
	config.Common.DependencyCaches = append([]projectenv.DependencyCacheConfig(nil), caches...)
	config.Codex.Arguments = append([]string(nil), defaults.Codex.Arguments...)
	return config
}
