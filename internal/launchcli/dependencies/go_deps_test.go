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

func TestGoCacheResolverUsesOneProbeAndCanonicalOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	build := filepath.Join(root, "build")
	modules := filepath.Join(root, "modules")
	for _, path := range []string{build, modules} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	resolver := goCacheResolver{
		run: func(context.Context, string, ...string) ([]byte, error) {
			calls++
			return []byte(`{"GOCACHE": "` + build + `", "GOMODCACHE": "` + modules + `"}`), nil
		},
		getenv:  func(string) string { return filepath.Join(root, "ignored-environment-value") },
		homeDir: root,
		stat:    os.Stat,
		access:  func(string, uint32) error { return nil },
	}
	kinds := []projectenv.DependencyCacheKind{
		projectenv.DependencyCacheGoModules,
		projectenv.DependencyCacheGoBuild,
	}
	got, err := resolver.resolve(context.Background(), kinds, false)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
	}
	want := []projectenv.DependencyCacheConfig{
		{Kind: projectenv.DependencyCacheGoBuild, Source: build},
		{Kind: projectenv.DependencyCacheGoModules, Source: modules},
	}
	if !reflect.DeepEqual(got.Caches, want) {
		t.Fatalf("caches = %#v, want %#v", got.Caches, want)
	}
}

func TestGoCacheResolverFallsBackOnlyWhenGoIsAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	build := filepath.Join(root, "build")
	if err := os.Mkdir(build, 0o700); err != nil {
		t.Fatal(err)
	}
	kinds := []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild}
	resolver := goCacheResolver{
		run: func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound },
		getenv: func(name string) string {
			if name == "GOCACHE" {
				return build
			}
			return ""
		},
		homeDir: root, stat: os.Stat, access: func(string, uint32) error { return nil },
	}
	if _, err := resolver.resolve(context.Background(), kinds, false); err != nil {
		t.Fatalf("missing go resolver error = %v", err)
	}
	resolver.run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("go failed") }
	if _, err := resolver.resolve(context.Background(), kinds, false); err == nil || !strings.Contains(err.Error(), "go failed") {
		t.Fatalf("failed go resolver error = %v", err)
	}
}

func TestGoCacheResolverModuleFallbackUsesFirstGOPATHEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	firstGOPATH := filepath.Join(root, "first")
	modules := filepath.Join(firstGOPATH, "pkg", "mod")
	if err := os.MkdirAll(modules, 0o700); err != nil {
		t.Fatal(err)
	}
	kinds := []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoModules}
	resolver := goCacheResolver{
		run: func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound },
		getenv: func(name string) string {
			if name == "GOPATH" {
				return strings.Join([]string{firstGOPATH, filepath.Join(root, "second")}, string(os.PathListSeparator))
			}
			return ""
		},
		homeDir: root, stat: os.Stat, access: func(string, uint32) error { return nil },
	}
	got, err := resolver.resolve(context.Background(), kinds, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []projectenv.DependencyCacheConfig{{Kind: projectenv.DependencyCacheGoModules, Source: modules}}
	if !reflect.DeepEqual(got.Caches, want) {
		t.Fatalf("caches = %#v, want %#v", got.Caches, want)
	}
}

func TestGoCacheResolverModuleFallbackPrefersGOMODCACHE(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	modules := filepath.Join(root, "modules")
	if err := os.Mkdir(modules, 0o700); err != nil {
		t.Fatal(err)
	}
	resolver := goCacheResolver{
		run: func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound },
		getenv: func(name string) string {
			switch name {
			case "GOMODCACHE":
				return modules
			case "GOPATH":
				return filepath.Join(root, "ignored-gopath")
			default:
				return ""
			}
		},
		homeDir: root, stat: os.Stat, access: func(string, uint32) error { return nil },
	}
	got, err := resolver.resolve(
		context.Background(),
		[]projectenv.DependencyCacheKind{projectenv.DependencyCacheGoModules},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []projectenv.DependencyCacheConfig{{Kind: projectenv.DependencyCacheGoModules, Source: modules}}
	if !reflect.DeepEqual(got.Caches, want) {
		t.Fatalf("caches = %#v, want %#v", got.Caches, want)
	}
}

func TestGoCacheResolverModuleFallbackDefaultsGOPATHToHomeGo(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	resolver := goCacheResolver{
		getenv:  func(string) string { return "" },
		homeDir: home,
	}
	want := filepath.Join(home, "go", "pkg", "mod")
	if got := resolver.fallbackPath(projectenv.DependencyCacheGoModules); got != want {
		t.Fatalf("fallbackPath(go_modules) = %q, want %q", got, want)
	}
}

func TestGoCacheResolverAutoOmitsButExplicitFails(t *testing.T) {
	t.Parallel()
	resolver := goCacheResolver{
		run:    func(context.Context, string, ...string) ([]byte, error) { return []byte(`not json`), nil },
		getenv: os.Getenv, homeDir: t.TempDir(), stat: os.Stat, access: func(string, uint32) error { return nil },
	}
	kinds := []projectenv.DependencyCacheKind{
		projectenv.DependencyCacheGoBuild,
		projectenv.DependencyCacheGoModules,
	}
	got, err := resolver.resolve(context.Background(), kinds, true)
	if err != nil || len(got.Caches) != 0 || len(got.Diagnostics) != 2 {
		t.Fatalf("auto = %#v, %v", got, err)
	}
	if _, err := resolver.resolve(context.Background(), kinds[:1], false); err == nil {
		t.Fatal("explicit selection accepted malformed probe")
	}
}

func TestGoCacheResolverNoneSkipsProbe(t *testing.T) {
	t.Parallel()
	resolver := newGoCacheResolver(t.TempDir())
	resolver.run = func(context.Context, string, ...string) ([]byte, error) { panic("probe must not run") }
	got, err := resolver.resolve(context.Background(), nil, false)
	if err != nil || got.Caches == nil || len(got.Caches) != 0 {
		t.Fatalf("resolve = %#v, %v", got, err)
	}
}

func TestGoCacheResolverRejectsNonGoKindWithoutProbe(t *testing.T) {
	t.Parallel()
	resolver := newGoCacheResolver(t.TempDir())
	resolver.run = func(context.Context, string, ...string) ([]byte, error) { panic("probe must not run") }
	_, err := resolver.resolve(context.Background(), []projectenv.DependencyCacheKind{"uv"}, false)
	if err == nil || !strings.Contains(err.Error(), "unsupported Go dependency cache kind") {
		t.Fatalf("resolve unsupported kind error = %v", err)
	}
}

func TestGoCacheResolverRejectsDuplicateKindWithoutProbe(t *testing.T) {
	t.Parallel()
	resolver := newGoCacheResolver(t.TempDir())
	resolver.run = func(context.Context, string, ...string) ([]byte, error) { panic("probe must not run") }
	kinds := []projectenv.DependencyCacheKind{
		projectenv.DependencyCacheGoBuild,
		projectenv.DependencyCacheGoBuild,
	}
	_, err := resolver.resolve(context.Background(), kinds, false)
	if err == nil || !strings.Contains(err.Error(), "duplicate Go dependency cache kind") {
		t.Fatalf("resolve duplicate kind error = %v", err)
	}
}
