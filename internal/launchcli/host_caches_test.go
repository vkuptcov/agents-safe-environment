package launchcli

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

func TestParseHostCacheSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  HostCacheSelection
		fail  bool
	}{
		{"", HostCacheSelection{Auto: true, Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules}}, false},
		{"auto", HostCacheSelection{Auto: true, Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules}}, false},
		{"none", HostCacheSelection{}, false},
		{"go_modules,go_build", HostCacheSelection{Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules}}, false},
		{"go_build,go_build", HostCacheSelection{}, true},
		{"auto,go_build", HostCacheSelection{}, true},
		{"uv", HostCacheSelection{}, true},
		{"go_build,", HostCacheSelection{}, true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := ParseHostCacheSelection(test.value)
			if test.fail {
				if err == nil {
					t.Fatal("ParseHostCacheSelection() succeeded")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ParseHostCacheSelection() = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

func TestHostCacheResolverUsesOneProbeAndCanonicalOrder(t *testing.T) {
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
	resolver := hostCacheResolver{
		run: func(context.Context, string, ...string) ([]byte, error) {
			calls++
			return []byte(`{"GOCACHE": "` + build + `", "GOMODCACHE": "` + modules + `"}`), nil
		},
		getenv: os.Getenv, homeDir: root, stat: os.Stat,
		access: func(string, uint32) error { return nil },
	}
	selection, err := ParseHostCacheSelection("go_modules,go_build")
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.resolve(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
	}
	want := []projectenv.DependencyCacheConfig{{Kind: projectenv.DependencyCacheGoBuild, Source: build}, {Kind: projectenv.DependencyCacheGoModules, Source: modules}}
	if !reflect.DeepEqual(got.Caches, want) {
		t.Fatalf("caches = %#v, want %#v", got.Caches, want)
	}
}

func TestHostCacheResolverFallsBackOnlyWhenGoIsAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	build := filepath.Join(root, "build")
	if err := os.Mkdir(build, 0o700); err != nil {
		t.Fatal(err)
	}
	selection, err := ParseHostCacheSelection("go_build")
	if err != nil {
		t.Fatal(err)
	}
	resolver := hostCacheResolver{
		run: func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound },
		getenv: func(name string) string {
			if name == "GOCACHE" {
				return build
			}
			return ""
		},
		homeDir: root, stat: os.Stat, access: func(string, uint32) error { return nil },
	}
	if _, err := resolver.resolve(context.Background(), selection); err != nil {
		t.Fatalf("missing go resolver error = %v", err)
	}
	resolver.run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("go failed") }
	if _, err := resolver.resolve(context.Background(), selection); err == nil || !strings.Contains(err.Error(), "go failed") {
		t.Fatalf("failed go resolver error = %v", err)
	}
}

func TestHostCacheResolverAutoOmitsButExplicitFails(t *testing.T) {
	t.Parallel()
	resolver := hostCacheResolver{
		run:    func(context.Context, string, ...string) ([]byte, error) { return []byte(`not json`), nil },
		getenv: os.Getenv, homeDir: t.TempDir(), stat: os.Stat, access: func(string, uint32) error { return nil },
	}
	auto, _ := ParseHostCacheSelection("auto")
	got, err := resolver.resolve(context.Background(), auto)
	if err != nil || len(got.Caches) != 0 || len(got.Diagnostics) != 2 {
		t.Fatalf("auto = %#v, %v", got, err)
	}
	explicit, _ := ParseHostCacheSelection("go_build")
	if _, err := resolver.resolve(context.Background(), explicit); err == nil {
		t.Fatal("explicit selection accepted malformed probe")
	}
}

func TestHostCacheResolverNoneSkipsProbe(t *testing.T) {
	t.Parallel()
	resolver := newHostCacheResolver(t.TempDir())
	resolver.run = func(context.Context, string, ...string) ([]byte, error) { panic("probe must not run") }
	got, err := resolver.resolve(context.Background(), HostCacheSelection{})
	if err != nil || got.Caches == nil || len(got.Caches) != 0 {
		t.Fatalf("resolve = %#v, %v", got, err)
	}
}
