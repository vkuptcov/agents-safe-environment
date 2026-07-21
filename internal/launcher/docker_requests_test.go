package launcher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestCreateRequestUsesOnlyResolvedPhysicalMounts(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Mounts, dockerMounts(plan.Mounts)) {
		t.Fatalf("mounts = %#v, want resolved plan %#v", request.Mounts, plan.Mounts)
	}
	fingerprintFound := false
	uvCacheFound := false
	for _, label := range request.Labels {
		if label.Key == codexHomeLabel && label.Value != "/home/developer/.codex" {
			t.Fatalf("Codex label = %q, want resolved source", label.Value)
		}
		if label.Key == launchConfigLabel && label.Value == "fingerprint" {
			fingerprintFound = true
		}
		if label.Key == uvCacheLabel && label.Value == mountAbsent {
			uvCacheFound = true
		}
	}
	if !fingerprintFound {
		t.Fatalf("labels = %#v, want %s", request.Labels, launchConfigLabel)
	}
	if !uvCacheFound {
		t.Fatalf("labels = %#v, want absent %s", request.Labels, uvCacheLabel)
	}
}

func TestCreateRequestLabelsResolvedUVCache(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.DependencyCaches = []launchplan.DependencyCache{{
		Kind: projectenv.DependencyCacheUV, Source: "/physical/uv-cache", Target: "/host/cache/uv",
	}}
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range request.Labels {
		if label.Key == uvCacheLabel && label.Value == "/physical/uv-cache" {
			return
		}
	}
	t.Fatalf("labels = %#v, want %s=/physical/uv-cache", request.Labels, uvCacheLabel)
}

func dockerMounts(mounts []launchplan.BindMount) []dockercli.Mount {
	result := make([]dockercli.Mount, 0, len(mounts))
	for _, mount := range mounts {
		result = append(result, dockercli.Mount{Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly})
	}
	return result
}

func TestExecRequestUsesResolvedCodexTarget(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.Provenance[3].Mount.Target = "/container/codex"
	plan.Mounts[3].Target = "/container/codex"
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildExecRequest(
		plan, []string{"true"}, strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "CODEX_HOME=/container/codex"; !containsKeyValue(request.Environment, want) {
		t.Fatalf("environment = %#v, want %q", request.Environment, want)
	}
}

func TestExecRequestRoutesEveryConfiguredDependencyCache(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.DependencyCaches = []launchplan.DependencyCache{
		{Kind: projectenv.DependencyCacheGoBuild, Source: "/physical/build", Target: "/host/cache/build"},
		{Kind: projectenv.DependencyCacheGoModules, Source: "/physical/modules", Target: "/host/go/pkg/mod"},
		{Kind: projectenv.DependencyCacheUV, Source: "/physical/uv", Target: "/host/cache/uv"},
	}
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildExecRequest(plan, []string{"go", "test"}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !containsKeyValue(request.Environment, "GOCACHE=/host/cache/build") ||
		!containsKeyValue(request.Environment, "GOMODCACHE=/host/go/pkg/mod") ||
		!containsKeyValue(request.Environment, "UV_CACHE_DIR=/host/cache/uv") {
		t.Fatalf("environment = %#v", request.Environment)
	}
}

func containsKeyValue(values []dockercli.KeyValue, want string) bool {
	for _, value := range values {
		if value.Key+"="+value.Value == want {
			return true
		}
	}
	return false
}
