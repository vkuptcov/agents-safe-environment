package launchcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launchcli/dependencies"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// HostCacheSelection is the parsed init-only cache choice.
type HostCacheSelection struct {
	Kinds []projectenv.DependencyCacheKind
	Auto  bool
}

// ParseHostCacheSelection accepts the deliberately small Go-only init surface.
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
		if token == "" || !dependencies.IsGoCacheKind(kind) {
			return HostCacheSelection{}, fmt.Errorf("--host-caches accepts only auto, none, go_build, and go_modules")
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

// HostCacheResolution is the persisted snapshot and non-fatal auto-discovery diagnostics.
type HostCacheResolution struct {
	Caches      []projectenv.DependencyCacheConfig
	Diagnostics []string
}

// ResolveHostCaches resolves one init snapshot without Docker or network access.
func ResolveHostCaches(ctx context.Context, selection HostCacheSelection, homeDir string) (HostCacheResolution, error) {
	resolved, err := dependencies.ResolveGoCaches(ctx, selection.Kinds, selection.Auto, homeDir)
	// HostCacheResolution and GoCacheResolution are field-identical; convert directly.
	// On error resolved is the zero value, so this returns an empty resolution plus err.
	return HostCacheResolution(resolved), err
}
