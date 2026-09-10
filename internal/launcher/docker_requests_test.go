package launcher

import (
	"path/filepath"
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
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Mounts, dockerMounts(plan.Mounts)) {
		t.Fatalf("mounts = %#v, want resolved plan %#v", request.Mounts, plan.Mounts)
	}
	wantVolume := []dockercli.VolumeMount{
		{Source: CodexInstallationVolume, Target: CodexInstallationRoot, ReadOnly: true},
		{Source: ClaudeInstallationVolume, Target: ClaudeInstallationRoot, ReadOnly: true},
	}
	if !reflect.DeepEqual(request.Volumes, wantVolume) {
		t.Fatalf("volumes = %#v, want %#v", request.Volumes, wantVolume)
	}
	wantTmpfs := []dockercli.TmpfsMount{{Target: filepath.Join(plan.ProjectRoot, ".venv"), Mode: projectenv.DefaultTmpfsMode}}
	if !reflect.DeepEqual(request.Tmpfs, wantTmpfs) {
		t.Fatalf("tmpfs = %#v, want %#v", request.Tmpfs, wantTmpfs)
	}
	wantBootstrapTmpfs := `[{"target":"/sources/feature worktree/.venv","mode":"1777"}]`
	if !containsKeyValue(request.Environment, tmpfsMountsEnvironment+"="+wantBootstrapTmpfs) {
		t.Fatalf("environment = %#v, want encoded bootstrap tmpfs plan", request.Environment)
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

func TestCreateRequestPreservesGuardedGitMountOrder(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.Mounts = []launchplan.BindMount{
		{Source: "/repo", Target: "/repo", ReadOnly: true},
		{Source: "/repo/.git", Target: "/repo/.git"},
		{Source: "/feature", Target: "/feature"},
		{Source: "/repo/.git/worktrees", Target: "/repo/.git/worktrees", ReadOnly: true},
		{Source: "/repo/.git/worktrees/feature", Target: "/repo/.git/worktrees/feature"},
	}
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := dockerMounts(plan.Mounts); !reflect.DeepEqual(request.Mounts, want) {
		t.Fatalf("mounts = %#v, want ordered guarded plan %#v", request.Mounts, want)
	}
}

func TestCreateRequestLabelsResolvedUVCache(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.DependencyCaches = []launchplan.DependencyCache{{
		Kind: projectenv.DependencyCacheUV, Source: "/physical/uv-cache", Target: "/host/cache/uv",
	}}
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", false,
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

func TestCreateRequestMasksDiscoveredPythonVirtualEnvironments(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.TmpfsMounts = []launchplan.TmpfsMount{
		{Target: filepath.Join(plan.ProjectRoot, ".venv"), Mode: projectenv.DefaultTmpfsMode},
		{Target: filepath.Join(plan.ProjectRoot, ".venv-dev"), Mode: "0755"},
		{Target: filepath.Join(plan.ProjectRoot, "venvs", "python311"), Mode: projectenv.DefaultTmpfsMode},
	}
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []dockercli.TmpfsMount{
		{Target: filepath.Join(plan.ProjectRoot, ".venv"), Mode: projectenv.DefaultTmpfsMode},
		{Target: filepath.Join(plan.ProjectRoot, ".venv-dev"), Mode: "0755"},
		{Target: filepath.Join(plan.ProjectRoot, "venvs", "python311"), Mode: projectenv.DefaultTmpfsMode},
	}
	if !reflect.DeepEqual(request.Tmpfs, want) {
		t.Fatalf("tmpfs = %#v, want %#v", request.Tmpfs, want)
	}
}

// Owned marks the virtual-environment masks. Those are the mounts whose native extension modules the
// dynamic loader must map executable; generic scratch targets keep the default restriction.
func TestCreateRequestMakesOnlyVirtualEnvironmentMasksExecutable(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.TmpfsMounts = []launchplan.TmpfsMount{
		{Target: filepath.Join(plan.ProjectRoot, ".venv"), Mode: projectenv.DefaultTmpfsMode, Owned: true},
		{Target: filepath.Join(plan.ProjectRoot, "scratch"), Mode: projectenv.DefaultTmpfsMode},
	}
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []dockercli.TmpfsMount{
		{Target: filepath.Join(plan.ProjectRoot, ".venv"), Mode: projectenv.DefaultTmpfsMode, Exec: true},
		{Target: filepath.Join(plan.ProjectRoot, "scratch"), Mode: projectenv.DefaultTmpfsMode},
	}
	if !reflect.DeepEqual(request.Tmpfs, want) {
		t.Fatalf("tmpfs = %#v, want %#v", request.Tmpfs, want)
	}
}

func TestCreateRequestOmitsTmpfsWhenHostVirtualEnvironmentsAreUsed(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.TmpfsMounts = nil
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Tmpfs) != 0 {
		t.Fatalf("tmpfs = %#v, want host virtual environments exposed", request.Tmpfs)
	}
	for _, environment := range request.Environment {
		if environment.Key == tmpfsMountsEnvironment {
			t.Fatalf("environment = %#v, want no bootstrap tmpfs plan", request.Environment)
		}
	}
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

func TestExecRequestUsesExplicitClaudeConfigDirectory(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	claude := launchplan.BindMount{Source: "/host/claude", Target: "/home/developer/.claude"}
	plan.Mounts = append(plan.Mounts, claude)
	plan.Provenance = append(plan.Provenance, launchplan.MountProvenance{
		Mount: claude, Roles: []projectenv.MountRole{projectenv.RoleClaudeHome},
	})
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildExecRequest(
		plan, []string{"true"}, strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "CLAUDE_CONFIG_DIR=/home/developer/.claude"; !containsKeyValue(request.Environment, want) {
		t.Fatalf("environment = %#v, want %q", request.Environment, want)
	}
}

func TestExecRequestKeepsDefaultClaudeSplitPaths(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	claude := launchplan.BindMount{Source: "/host/.claude", Target: "/home/developer/.claude"}
	config := launchplan.BindMount{Source: "/host/.claude.json", Target: "/home/developer/.claude.json"}
	plan.Mounts = append(plan.Mounts, claude, config)
	plan.Provenance = append(plan.Provenance,
		launchplan.MountProvenance{Mount: claude, Roles: []projectenv.MountRole{projectenv.RoleClaudeHome}},
		launchplan.MountProvenance{Mount: config, Roles: []projectenv.MountRole{projectenv.RoleClaudeConfig}},
	)
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildExecRequest(
		plan, []string{"true"}, strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range request.Environment {
		if environment.Key == "CLAUDE_CONFIG_DIR" {
			t.Fatalf("default split paths must not set CLAUDE_CONFIG_DIR: %#v", request.Environment)
		}
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

func TestCreateRequestCarriesKeepContainer(t *testing.T) {
	t.Parallel()
	launcher := hostLauncher(1000, 1001, "/home/developer", "")
	kept, err := launcher.buildCreateRequest(testPlan(), "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", true)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.KeepContainer {
		t.Fatal("KeepContainer = false, want the option forwarded to Docker create")
	}
	removed, err := launcher.buildCreateRequest(testPlan(), "image", "codex-safe-test", hostMCPPlan{}, "fingerprint", false)
	if err != nil {
		t.Fatal(err)
	}
	if removed.KeepContainer {
		t.Fatal("KeepContainer = true, want default auto-removal")
	}
}
