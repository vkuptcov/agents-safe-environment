package launchcli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launchcli/dependencies"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestParseHostCacheSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  HostCacheSelection
		fail  bool
	}{
		{"", HostCacheSelection{Auto: true, Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules, projectenv.DependencyCacheUV}}, false},
		{"auto", HostCacheSelection{Auto: true, Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules, projectenv.DependencyCacheUV}}, false},
		{"none", HostCacheSelection{}, false},
		{"uv,go_modules,go_build", HostCacheSelection{Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild, projectenv.DependencyCacheGoModules, projectenv.DependencyCacheUV}}, false},
		{"go_build,go_build", HostCacheSelection{}, true},
		{"auto,go_build", HostCacheSelection{}, true},
		{"maven", HostCacheSelection{}, true},
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

func TestResolveHostCachesOrdersCandidatesAndOmitsUnsafeAutoCandidate(t *testing.T) {
	t.Parallel()
	goCache := projectenv.DependencyCacheConfig{Kind: projectenv.DependencyCacheGoBuild, Source: "/cache/go-build"}
	uvCache := projectenv.DependencyCacheConfig{Kind: projectenv.DependencyCacheUV, Source: "/cache/uv"}
	got, err := resolveHostCaches(
		context.Background(),
		HostCacheSelection{Auto: true, Kinds: []projectenv.DependencyCacheKind{
			projectenv.DependencyCacheUV,
			projectenv.DependencyCacheGoBuild,
		}},
		"/project",
		"/home/developer",
		hostCacheResolutionInputs{
			resolveGo: func(_ context.Context, kinds []projectenv.DependencyCacheKind, auto bool, home string) (dependencies.GoCacheResolution, error) {
				if !auto || home != "/home/developer" || !reflect.DeepEqual(kinds, []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild}) {
					t.Fatalf("Go resolver input = %#v, %t, %q", kinds, auto, home)
				}
				return dependencies.GoCacheResolution{Caches: []projectenv.DependencyCacheConfig{goCache}}, nil
			},
			resolveUV: func(_ context.Context, auto bool, home string, projectRoot string) (dependencies.UVCacheResolution, error) {
				if !auto || home != "/home/developer" || projectRoot != "/project" {
					t.Fatalf("uv resolver input = %t, %q, %q", auto, home, projectRoot)
				}
				return dependencies.UVCacheResolution{Caches: []projectenv.DependencyCacheConfig{uvCache}}, nil
			},
			validate: func(caches []projectenv.DependencyCacheConfig) error {
				if len(caches) == 2 {
					return errors.New("cache overlaps project")
				}
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := []projectenv.DependencyCacheConfig{goCache}; !reflect.DeepEqual(got.Caches, want) {
		t.Fatalf("caches = %#v, want %#v", got.Caches, want)
	}
	if want := []string{"uv unavailable: cache overlaps project"}; !reflect.DeepEqual(got.Diagnostics, want) {
		t.Fatalf("diagnostics = %#v, want %#v", got.Diagnostics, want)
	}
}

func TestResolveHostCachesExplicitUnsafeCandidateFailsAndNoneSkipsProbes(t *testing.T) {
	t.Parallel()
	inputs := hostCacheResolutionInputs{
		resolveGo: func(context.Context, []projectenv.DependencyCacheKind, bool, string) (dependencies.GoCacheResolution, error) {
			panic("Go probe must not run")
		},
		resolveUV: func(context.Context, bool, string, string) (dependencies.UVCacheResolution, error) {
			return dependencies.UVCacheResolution{Caches: []projectenv.DependencyCacheConfig{{
				Kind: projectenv.DependencyCacheUV, Source: "/cache/uv",
			}}}, nil
		},
		validate: func(caches []projectenv.DependencyCacheConfig) error {
			if len(caches) != 0 {
				return errors.New("cache overlaps project")
			}
			return nil
		},
	}
	if _, err := resolveHostCaches(context.Background(), HostCacheSelection{
		Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheUV},
	}, "/project", "/home/developer", inputs); err == nil || !strings.Contains(err.Error(), "cache overlaps project") {
		t.Fatalf("explicit unsafe cache error = %v", err)
	}

	inputs.resolveUV = func(context.Context, bool, string, string) (dependencies.UVCacheResolution, error) {
		panic("uv probe must not run")
	}
	got, err := resolveHostCaches(context.Background(), HostCacheSelection{}, "/project", "/home/developer", inputs)
	if err != nil || got.Caches == nil || len(got.Caches) != 0 {
		t.Fatalf("none = %#v, %v", got, err)
	}
}

func TestResolveHostCachesDoesNotProbeUnselectedFamily(t *testing.T) {
	t.Parallel()
	goCache := projectenv.DependencyCacheConfig{Kind: projectenv.DependencyCacheGoBuild, Source: "/cache/go-build"}
	got, err := resolveHostCaches(
		context.Background(),
		HostCacheSelection{Kinds: []projectenv.DependencyCacheKind{projectenv.DependencyCacheGoBuild}},
		"/project",
		"/home/developer",
		hostCacheResolutionInputs{
			resolveGo: func(context.Context, []projectenv.DependencyCacheKind, bool, string) (dependencies.GoCacheResolution, error) {
				return dependencies.GoCacheResolution{Caches: []projectenv.DependencyCacheConfig{goCache}}, nil
			},
			resolveUV: func(context.Context, bool, string, string) (dependencies.UVCacheResolution, error) {
				panic("uv probe must not run")
			},
			validate: func([]projectenv.DependencyCacheConfig) error { return nil },
		},
	)
	if err != nil || !reflect.DeepEqual(got.Caches, []projectenv.DependencyCacheConfig{goCache}) {
		t.Fatalf("Go-only resolution = %#v, %v", got, err)
	}
}
