package launchcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const hostCacheProbeTimeout = 5 * time.Second

const (
	accessRead   = 4
	accessWrite  = 2
	accessSearch = 1
)

// HostCacheSelection is the parsed init-only cache choice.
type HostCacheSelection struct {
	Kinds []projectenv.DependencyCacheKind
	Auto  bool
}

// ParseHostCacheSelection accepts the deliberately small Go-only init surface.
func ParseHostCacheSelection(value string) (HostCacheSelection, error) {
	if value == "" || value == "auto" {
		return HostCacheSelection{Auto: true, Kinds: []projectenv.DependencyCacheKind{
			projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules,
		}}, nil
	}
	if value == "none" {
		return HostCacheSelection{}, nil
	}
	seen := map[projectenv.DependencyCacheKind]bool{}
	selection := HostCacheSelection{}
	for _, token := range strings.Split(value, ",") {
		kind := projectenv.DependencyCacheKind(token)
		if token == "" || (kind != projectenv.DependencyCacheGoBuild && kind != projectenv.DependencyCacheGoModules) {
			return HostCacheSelection{}, fmt.Errorf("--host-caches accepts only auto, none, go_build, and go_modules")
		}
		if seen[kind] {
			return HostCacheSelection{}, fmt.Errorf("--host-caches repeats %q", kind)
		}
		seen[kind] = true
	}
	for _, kind := range []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules} {
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

type hostCacheCommand func(context.Context, string, ...string) ([]byte, error)

type hostCacheResolver struct {
	run     hostCacheCommand
	getenv  func(string) string
	homeDir string
	stat    func(string) (os.FileInfo, error)
	access  func(string, uint32) error
}

// ResolveHostCaches resolves one init snapshot without Docker or network access.
func ResolveHostCaches(ctx context.Context, selection HostCacheSelection, homeDir string) (HostCacheResolution, error) {
	return newHostCacheResolver(homeDir).resolve(ctx, selection)
}

func newHostCacheResolver(homeDir string) hostCacheResolver {
	return hostCacheResolver{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		getenv:  os.Getenv,
		homeDir: homeDir,
		stat:    os.Stat,
		access:  syscall.Access,
	}
}

func (resolver hostCacheResolver) resolve(ctx context.Context, selection HostCacheSelection) (HostCacheResolution, error) {
	if len(selection.Kinds) == 0 {
		return HostCacheResolution{}, nil
	}
	probeContext, cancel := context.WithTimeout(ctx, hostCacheProbeTimeout)
	defer cancel()
	output, err := resolver.run(probeContext, "go", "env", "-json", "GOCACHE", "GOMODCACHE")
	values := map[projectenv.DependencyCacheKind]string{}
	missingGo := errors.Is(err, exec.ErrNotFound)
	if err == nil {
		var parsed struct {
			GOCACHE    string
			GOMODCACHE string
		}
		if decodeErr := json.Unmarshal(output, &parsed); decodeErr != nil {
			err = fmt.Errorf("parse go env output: %w", decodeErr)
		} else {
			values[projectenv.DependencyCacheGoBuild] = parsed.GOCACHE
			values[projectenv.DependencyCacheGoModules] = parsed.GOMODCACHE
		}
	}

	result := HostCacheResolution{}
	for _, kind := range selection.Kinds {
		path, resolveErr := resolver.cachePath(kind, values[kind], missingGo, err)
		if resolveErr == nil {
			result.Caches = append(result.Caches, projectenv.DependencyCacheConfig{Kind: kind, Source: path})
			continue
		}
		if !selection.Auto {
			return HostCacheResolution{}, resolveErr
		}
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("%s unavailable: %v", kind, resolveErr))
	}
	if len(result.Diagnostics) != 0 && len(result.Caches) == 0 {
		return result, nil
	}
	return result, nil
}

func (resolver hostCacheResolver) cachePath(
	kind projectenv.DependencyCacheKind,
	probed string,
	missingGo bool,
	probeErr error,
) (string, error) {
	path := probed
	if probeErr != nil {
		if !missingGo {
			return "", fmt.Errorf("run go env: %w", probeErr)
		}
		path = resolver.fallbackPath(kind)
	}
	if path == "" || path == "off" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("resolved path %q is not a canonical absolute directory", path)
	}
	if err := projectenv.ValidatePath("resolved Go cache", path, true); err != nil {
		return "", err
	}
	info, err := resolver.stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	if err := resolver.access(path, accessRead|accessWrite|accessSearch); err != nil {
		return "", fmt.Errorf("access %q: %w", path, err)
	}
	return path, nil
}

func (resolver hostCacheResolver) fallbackPath(kind projectenv.DependencyCacheKind) string {
	if kind == projectenv.DependencyCacheGoBuild {
		if value := resolver.getenv("GOCACHE"); value != "" {
			return value
		}
		if xdg := resolver.getenv("XDG_CACHE_HOME"); xdg != "" {
			return filepath.Join(xdg, "go-build")
		}
		return filepath.Join(resolver.homeDir, ".cache", "go-build")
	}
	if value := resolver.getenv("GOMODCACHE"); value != "" {
		return value
	}
	return filepath.Join(resolver.homeDir, "go", "pkg", "mod")
}
