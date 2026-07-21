package launchcli

import (
	"reflect"
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
