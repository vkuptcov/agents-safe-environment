package dependencies

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestUVCacheResolverUsesProjectEffectiveProbe(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := filepath.Join(root, "uv cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	resolver := uvCacheResolver{
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "uv" {
				t.Fatalf("command = %q, want uv", name)
			}
			if want := []string{"cache", "dir", "--directory", filepath.Join(root, "project")}; !reflect.DeepEqual(args, want) {
				t.Fatalf("args = %#v, want %#v", args, want)
			}
			return []byte(cache + "\r\n"), nil
		},
		getenv:      func(string) string { return "" },
		homeDir:     root,
		projectRoot: filepath.Join(root, "project"),
		stat:        os.Stat,
		access:      func(string, uint32) error { return nil },
	}
	got, err := resolver.resolve(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := []projectenv.DependencyCacheConfig{{Kind: projectenv.DependencyCacheUV, Source: cache}}
	if !reflect.DeepEqual(got.Caches, want) {
		t.Fatalf("caches = %#v, want %#v", got.Caches, want)
	}
}

func TestUVCacheResolverFallsBackOnlyWhenUVIsAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := filepath.Join(root, "configured-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	resolver := uvCacheResolver{
		run: func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound },
		getenv: func(name string) string {
			if name == "UV_CACHE_DIR" {
				return cache
			}
			return ""
		},
		homeDir:     root,
		projectRoot: root,
		stat:        os.Stat,
		access:      func(string, uint32) error { return nil },
	}
	if _, err := resolver.resolve(context.Background(), false); err != nil {
		t.Fatalf("missing uv resolver error = %v", err)
	}
	resolver.run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("uv failed") }
	if _, err := resolver.resolve(context.Background(), false); err == nil || !strings.Contains(err.Error(), "uv failed") {
		t.Fatalf("failed uv resolver error = %v", err)
	}
}

func TestUVCacheResolverFallbackOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	resolver := uvCacheResolver{homeDir: root}
	resolver.getenv = func(name string) string {
		switch name {
		case "UV_CACHE_DIR":
			return filepath.Join(root, "explicit")
		case "XDG_CACHE_HOME":
			return filepath.Join(root, "xdg")
		default:
			return ""
		}
	}
	if got, want := resolver.fallbackPath(), filepath.Join(root, "explicit"); got != want {
		t.Fatalf("fallbackPath() = %q, want %q", got, want)
	}
	resolver.getenv = func(name string) string {
		if name == "XDG_CACHE_HOME" {
			return filepath.Join(root, "xdg")
		}
		return ""
	}
	if got, want := resolver.fallbackPath(), filepath.Join(root, "xdg", "uv"); got != want {
		t.Fatalf("fallbackPath() = %q, want %q", got, want)
	}
	resolver.getenv = func(string) string { return "" }
	if got, want := resolver.fallbackPath(), filepath.Join(root, ".cache", "uv"); got != want {
		t.Fatalf("fallbackPath() = %q, want %q", got, want)
	}
}

func TestUVCacheResolverRejectsRelativeFallbackPath(t *testing.T) {
	t.Parallel()
	resolver := uvCacheResolver{
		run:         func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound },
		getenv:      func(string) string { return "relative-cache" },
		homeDir:     t.TempDir(),
		projectRoot: t.TempDir(),
		stat:        os.Stat,
		access:      func(string, uint32) error { return nil },
	}
	if _, err := resolver.resolve(context.Background(), false); err == nil || !strings.Contains(err.Error(), "canonical absolute") {
		t.Fatalf("relative fallback error = %v", err)
	}
}

func TestUVCacheResolverDoesNotFallbackAfterProbeFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := filepath.Join(root, "fallback-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, probeErr := range []error{context.DeadlineExceeded, context.Canceled, errors.New("uv exited 2")} {
		t.Run(probeErr.Error(), func(t *testing.T) {
			resolver := uvCacheResolver{
				run:         func(context.Context, string, ...string) ([]byte, error) { return nil, probeErr },
				getenv:      func(string) string { return cache },
				homeDir:     root,
				projectRoot: root,
				stat:        os.Stat,
				access:      func(string, uint32) error { return nil },
			}
			if _, err := resolver.resolve(context.Background(), false); err == nil || !strings.Contains(err.Error(), probeErr.Error()) {
				t.Fatalf("explicit probe error = %v", err)
			}
			got, err := resolver.resolve(context.Background(), true)
			if err != nil || len(got.Caches) != 0 || len(got.Diagnostics) != 1 || !strings.Contains(got.Diagnostics[0], probeErr.Error()) {
				t.Fatalf("auto probe result = %#v, %v", got, err)
			}
		})
	}
}

func TestUVCacheResolverAutoOmitsInvalidPathsButExplicitFails(t *testing.T) {
	t.Parallel()
	resolver := uvCacheResolver{
		run:         func(context.Context, string, ...string) ([]byte, error) { return []byte("relative\npath\n"), nil },
		getenv:      func(string) string { return "" },
		homeDir:     t.TempDir(),
		projectRoot: t.TempDir(),
		stat:        os.Stat,
		access:      func(string, uint32) error { return nil },
	}
	got, err := resolver.resolve(context.Background(), true)
	if err != nil || len(got.Caches) != 0 || len(got.Diagnostics) != 1 {
		t.Fatalf("auto = %#v, %v", got, err)
	}
	if _, err := resolver.resolve(context.Background(), false); err == nil {
		t.Fatal("explicit selection accepted malformed output")
	}
}

func TestUVCacheResolverRejectsDisappearedAndInaccessiblePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	missing := filepath.Join(root, "temporary-cache")
	resolver := uvCacheResolver{
		run:         func(context.Context, string, ...string) ([]byte, error) { return []byte(missing + "\n"), nil },
		getenv:      func(string) string { return "" },
		homeDir:     root,
		projectRoot: root,
		stat:        os.Stat,
		access:      func(string, uint32) error { return nil },
	}
	if _, err := resolver.resolve(context.Background(), false); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatalf("disappeared path error = %v", err)
	}
	if err := os.Mkdir(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	resolver.access = func(string, uint32) error { return errors.New("permission denied") }
	if _, err := resolver.resolve(context.Background(), false); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("access error = %v", err)
	}
}
