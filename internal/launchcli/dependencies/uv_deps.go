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
// Warnings are operator-facing notices that are always surfaced (including at launch); Diagnostics are
// routine "cache unavailable" notes that only init prints.
type UVCacheResolution struct {
	Caches      []projectenv.DependencyCacheConfig
	Diagnostics []string
	Warnings    []string
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

// resolve computes the host uv cache to mount. Automatic discovery must never let project-controlled uv
// configuration choose the mounted host directory: a checked-in uv.toml with, for example,
// `cache-dir = "/run/user/1000"` would otherwise be bind-mounted read-write into the container and expose an
// arbitrary host location (a rootless Docker socket, a credential directory) — defeating the sandbox. We
// therefore resolve the mounted path with `uv cache dir --no-config`, which ignores every uv configuration
// file (project and user) and honors only UV_CACHE_DIR/XDG/platform defaults. When the effective uv
// configuration would have selected a different directory, we surface a non-fatal warning so the operator can
// review it and, if they want it, record it explicitly in .agents-safe/config.toml. Automatic discovery never
// mounts it on their behalf.
func (resolver uvCacheResolver) resolve(ctx context.Context, auto bool) (UVCacheResolution, error) {
	path, err := resolver.trustedCachePath(ctx)
	if err == nil {
		err = validateExistingCacheDirectory("resolved uv cache", path, resolver.stat, resolver.access)
	}
	if err != nil {
		if !auto {
			return UVCacheResolution{}, err
		}
		return UVCacheResolution{Diagnostics: []string{fmt.Sprintf("uv unavailable: %v", err)}}, nil
	}
	resolution := UVCacheResolution{Caches: []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheUV, Source: path,
	}}}
	if warning := resolver.configOverrideWarning(ctx, path); warning != "" {
		resolution.Warnings = []string{warning}
	}
	return resolution, nil
}

// trustedCachePath returns the cache directory uv would use with all configuration files ignored.
func (resolver uvCacheResolver) trustedCachePath(ctx context.Context) (string, error) {
	output, err := resolver.runCacheDir(ctx, "--no-config")
	if errors.Is(err, exec.ErrNotFound) {
		return resolver.fallbackPath(), nil
	}
	if err != nil {
		return "", fmt.Errorf("run uv cache dir: %w", err)
	}
	return parseUVCachePath(output)
}

// configOverrideWarning reports, best-effort, when uv configuration would select a cache directory other than
// the trusted one we mount. It reads configuration (via --directory, so a project uv.toml participates) only to
// compare paths; the returned directory is never mounted. Any probe failure is treated as "no override" because
// a missing or broken uv is already handled by trustedCachePath.
func (resolver uvCacheResolver) configOverrideWarning(ctx context.Context, trusted string) string {
	output, err := resolver.runCacheDir(ctx, "--directory", resolver.projectRoot)
	if err != nil {
		return ""
	}
	configured, err := parseUVCachePath(output)
	if err != nil || configured == "" || configured == trusted {
		return ""
	}
	return fmt.Sprintf(
		"uv configuration resolves cache dir %q, which differs from the safe default %q; automatic discovery "+
			"did not mount it — add it to .agents-safe/config.toml if you want it mounted",
		configured, trusted,
	)
}

func (resolver uvCacheResolver) runCacheDir(ctx context.Context, args ...string) ([]byte, error) {
	probeContext, cancel := context.WithTimeout(ctx, uvCacheProbeTimeout)
	defer cancel()
	return resolver.run(probeContext, "uv", append([]string{"cache", "dir"}, args...)...)
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
