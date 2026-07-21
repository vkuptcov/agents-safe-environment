package dependencies

import (
	"context"
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

const uvCacheProbeTimeout = 5 * time.Second

// UVCacheResolution contains the persisted uv cache snapshot and non-fatal auto-discovery diagnostics.
type UVCacheResolution struct {
	Caches      []projectenv.DependencyCacheConfig
	Diagnostics []string
}

type uvCacheCommand func(context.Context, string, ...string) ([]byte, error)

type uvCacheResolver struct {
	run         uvCacheCommand
	getenv      func(string) string
	homeDir     string
	projectRoot string
	stat        func(string) (os.FileInfo, error)
	access      func(string, uint32) error
}

// ResolveUVCache resolves one init snapshot for the host uv dependency cache.
func ResolveUVCache(
	ctx context.Context,
	auto bool,
	homeDir string,
	projectRoot string,
) (UVCacheResolution, error) {
	return newUVCacheResolver(homeDir, projectRoot).resolve(ctx, auto)
}

func newUVCacheResolver(homeDir string, projectRoot string) uvCacheResolver {
	return uvCacheResolver{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		getenv:      os.Getenv,
		homeDir:     homeDir,
		projectRoot: projectRoot,
		stat:        os.Stat,
		access:      syscall.Access,
	}
}

func (resolver uvCacheResolver) resolve(ctx context.Context, auto bool) (UVCacheResolution, error) {
	path, err := resolver.cachePath(ctx)
	if err == nil {
		err = validateExistingCacheDirectory("resolved uv cache", path, resolver.stat, resolver.access)
	}
	if err != nil {
		if !auto {
			return UVCacheResolution{}, err
		}
		return UVCacheResolution{Diagnostics: []string{fmt.Sprintf("uv unavailable: %v", err)}}, nil
	}
	return UVCacheResolution{Caches: []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheUV, Source: path,
	}}}, nil
}

func (resolver uvCacheResolver) cachePath(ctx context.Context) (string, error) {
	probeContext, cancel := context.WithTimeout(ctx, uvCacheProbeTimeout)
	defer cancel()
	output, err := resolver.run(probeContext, "uv", "cache", "dir", "--directory", resolver.projectRoot)
	if errors.Is(err, exec.ErrNotFound) {
		return resolver.fallbackPath(), nil
	}
	if err != nil {
		return "", fmt.Errorf("run uv cache dir: %w", err)
	}
	return parseUVCachePath(output)
}

func parseUVCachePath(output []byte) (string, error) {
	path := strings.TrimSuffix(string(output), "\n")
	path = strings.TrimSuffix(path, "\r")
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return "", errors.New("uv cache dir must return exactly one path")
	}
	return path, nil
}

func (resolver uvCacheResolver) fallbackPath() string {
	if value := resolver.getenv("UV_CACHE_DIR"); value != "" {
		return value
	}
	if xdg := resolver.getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "uv")
	}
	return filepath.Join(resolver.homeDir, ".cache", "uv")
}
