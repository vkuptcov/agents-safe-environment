// Package dependencies resolves host dependency state for launchcli without Docker or network access.
package dependencies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const goCacheProbeTimeout = 5 * time.Second

const (
	accessRead   = 4
	accessWrite  = 2
	accessSearch = 1
)

// GoCacheResolution contains the persisted Go cache snapshot and non-fatal auto-discovery diagnostics.
type GoCacheResolution struct {
	Caches      []projectenv.DependencyCacheConfig
	Diagnostics []string
}

type goCacheCommand func(context.Context, string, ...string) ([]byte, error)

type goCacheResolver struct {
	run     goCacheCommand
	getenv  func(string) string
	homeDir string
	stat    func(string) (os.FileInfo, error)
	access  func(string, uint32) error
}

// IsGoCacheKind reports whether kind is resolved by the Go dependency detector.
func IsGoCacheKind(kind projectenv.DependencyCacheKind) bool {
	return kind == projectenv.DependencyCacheGoBuild || kind == projectenv.DependencyCacheGoModules
}

// ResolveGoCaches resolves one init snapshot for the selected Go dependency cache kinds.
func ResolveGoCaches(
	ctx context.Context,
	kinds []projectenv.DependencyCacheKind,
	auto bool,
	homeDir string,
) (GoCacheResolution, error) {
	return newGoCacheResolver(homeDir).resolve(ctx, kinds, auto)
}

func newGoCacheResolver(homeDir string) goCacheResolver {
	return goCacheResolver{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		getenv:  os.Getenv,
		homeDir: homeDir,
		stat:    os.Stat,
		access:  syscall.Access,
	}
}

func (resolver goCacheResolver) resolve(
	ctx context.Context,
	kinds []projectenv.DependencyCacheKind,
	auto bool,
) (GoCacheResolution, error) {
	normalizedKinds, err := normalizeGoCacheKinds(kinds)
	if err != nil {
		return GoCacheResolution{}, err
	}
	if len(normalizedKinds) == 0 {
		return GoCacheResolution{Caches: []projectenv.DependencyCacheConfig{}}, nil
	}
	probeContext, cancel := context.WithTimeout(ctx, goCacheProbeTimeout)
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

	result := GoCacheResolution{}
	for _, kind := range normalizedKinds {
		path, resolveErr := resolver.cachePath(kind, values[kind], missingGo, err)
		if resolveErr == nil {
			result.Caches = append(result.Caches, projectenv.DependencyCacheConfig{Kind: kind, Source: path})
			continue
		}
		if !auto {
			return GoCacheResolution{}, resolveErr
		}
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("%s unavailable: %v", kind, resolveErr))
	}
	return result, nil
}

func normalizeGoCacheKinds(kinds []projectenv.DependencyCacheKind) ([]projectenv.DependencyCacheKind, error) {
	selected := make(map[projectenv.DependencyCacheKind]bool, len(kinds))
	for _, kind := range kinds {
		if !IsGoCacheKind(kind) {
			return nil, fmt.Errorf("unsupported Go dependency cache kind %q", kind)
		}
		if selected[kind] {
			return nil, fmt.Errorf("duplicate Go dependency cache kind %q", kind)
		}
		selected[kind] = true
	}

	normalized := make([]projectenv.DependencyCacheKind, 0, len(kinds))
	for _, kind := range projectenv.DependencyCacheKindOrder {
		if selected[kind] {
			normalized = append(normalized, kind)
		}
	}
	return normalized, nil
}

func (resolver goCacheResolver) cachePath(
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
	if path == "off" {
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

func (resolver goCacheResolver) fallbackPath(kind projectenv.DependencyCacheKind) string {
	if kind == projectenv.DependencyCacheGoBuild {
		if value := resolver.getenv("GOCACHE"); value != "" {
			return value
		}
		if xdg := resolver.getenv("XDG_CACHE_HOME"); xdg != "" {
			return filepath.Join(xdg, "go-build")
		}
		return filepath.Join(resolver.homeDir, ".cache", "go-build")
	}
	return resolver.moduleCacheFallbackPath()
}

func (resolver goCacheResolver) moduleCacheFallbackPath() string {
	if value := resolver.getenv("GOMODCACHE"); value != "" {
		return value
	}
	gopath := resolver.getenv("GOPATH")
	if gopath == "" {
		gopath = resolver.defaultGOPATH()
	}
	entries := filepath.SplitList(gopath)
	if len(entries) == 0 || entries[0] == "" {
		return ""
	}
	return filepath.Join(entries[0], "pkg", "mod")
}

func (resolver goCacheResolver) defaultGOPATH() string {
	return filepath.Join(resolver.homeDir, "go")
}
