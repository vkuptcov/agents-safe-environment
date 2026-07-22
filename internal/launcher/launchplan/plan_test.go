package launchplan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestResolveRegularCheckoutNormalizesRequiredRoles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{
		RequestedDir: root,
		WorktreeRoot: root,
		PrimaryRoot:  root,
		CommonGitDir: gitDir,
	}
	defaults := resolvedConfig(project, false)
	resolution, err := Resolve(project, defaults, defaults)
	if err != nil {
		t.Fatal(err)
	}
	wantMounts := []BindMount{{Source: root, Target: root}}
	if !reflect.DeepEqual(resolution.Plan.Mounts, wantMounts) {
		t.Fatalf("Mounts = %#v, want %#v", resolution.Plan.Mounts, wantMounts)
	}
	wantRoles := []projectenv.MountRole{
		projectenv.RolePrimaryCheckout,
		projectenv.RoleWorktree,
		projectenv.RoleCommonGitDir,
	}
	if !reflect.DeepEqual(resolution.Plan.Provenance[0].Roles, wantRoles) {
		t.Errorf("roles = %#v, want %#v", resolution.Plan.Provenance[0].Roles, wantRoles)
	}
	if !resolution.Plan.HostMCPChannel {
		t.Fatal("HostMCPChannel = false, want retained logical channel role")
	}

	withoutChannel := defaults
	withoutChannel.Common.Mounts = append(
		[]projectenv.MountConfig(nil),
		defaults.Common.Mounts[:len(defaults.Common.Mounts)-1]...,
	)
	resolution, err = Resolve(project, defaults, withoutChannel)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Plan.HostMCPChannel {
		t.Fatal("HostMCPChannel = true after channel role was omitted")
	}
	if want := []Degradation{{Role: projectenv.RoleHostMCPChannel}}; !reflect.DeepEqual(
		resolution.Degradations,
		want,
	) {
		t.Fatalf("Degradations = %#v, want %#v", resolution.Degradations, want)
	}
}

func TestResolveLinkedWorktreePreservesNestedWritableGitMount(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	worktree := filepath.Join(base, "worktree")
	commonGit := filepath.Join(primary, ".git")
	for _, path := range []string{primary, worktree, commonGit} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	project := gitproject.Project{
		RequestedDir: worktree,
		WorktreeRoot: worktree,
		PrimaryRoot:  primary,
		CommonGitDir: commonGit,
		Linked:       true,
	}
	defaults := resolvedConfig(project, true)
	resolution, err := Resolve(project, defaults, defaults)
	if err != nil {
		t.Fatal(err)
	}
	want := []BindMount{
		{Source: primary, Target: primary, ReadOnly: true},
		{Source: commonGit, Target: commonGit},
		{Source: worktree, Target: worktree},
	}
	if !reflect.DeepEqual(resolution.Plan.Mounts, want) {
		t.Fatalf("Mounts = %#v, want %#v", resolution.Plan.Mounts, want)
	}
}

func TestResolveDependencyCachesPreservesTargetAndUsesPhysicalSource(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(base, "project")
	gitDir := filepath.Join(root, ".git")
	physical := filepath.Join(home, "real-go-build")
	alias := filepath.Join(home, "cache-alias")
	modules := filepath.Join(home, "go", "pkg", "mod")
	uvCache := filepath.Join(home, ".cache", "uv")
	for _, path := range []string{home, root, gitDir, physical, modules, uvCache} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(physical, alias); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root, CommonGitDir: gitDir}
	defaults := resolvedConfig(project, false)
	config := defaults
	config.Common.DependencyCaches = []projectenv.DependencyCacheConfig{
		{Kind: projectenv.DependencyCacheUV, Source: uvCache},
		{Kind: projectenv.DependencyCacheGoModules, Source: modules},
		{Kind: projectenv.DependencyCacheGoBuild, Source: alias},
	}
	resolution, err := ResolveWithHostHome(project, defaults, config, home)
	if err != nil {
		t.Fatal(err)
	}
	want := []DependencyCache{
		{Kind: projectenv.DependencyCacheGoBuild, Source: physical, Target: alias},
		{Kind: projectenv.DependencyCacheGoModules, Source: modules, Target: modules},
		{Kind: projectenv.DependencyCacheUV, Source: uvCache, Target: uvCache},
	}
	if !reflect.DeepEqual(resolution.Plan.DependencyCaches, want) {
		t.Fatalf("DependencyCaches = %#v, want %#v", resolution.Plan.DependencyCaches, want)
	}
	if got := resolution.Plan.Mounts[len(resolution.Plan.Mounts)-3:]; !reflect.DeepEqual(got, []BindMount{
		{Source: physical, Target: alias}, {Source: modules, Target: modules}, {Source: uvCache, Target: uvCache},
	}) {
		t.Fatalf("cache mounts = %#v", got)
	}
}

func TestResolvePythonVirtualEnvironmentsUsesDiscoveredTargetsUnlessHostUseIsEnabled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	environments := []string{filepath.Join(root, ".venv"), filepath.Join(root, "service", ".venv")}
	for _, path := range append([]string{gitDir}, environments...) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range environments {
		if err := os.WriteFile(filepath.Join(path, pythonVenvMarker), []byte("home = /usr/bin\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	project := gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root, CommonGitDir: gitDir}
	defaults := resolvedConfig(project, false)
	defaults.Common.TmpfsMounts = []projectenv.TmpfsMountConfig{{
		Target: environments[0], Mode: projectenv.DefaultTmpfsMode,
	}}
	config := defaults
	resolution, err := Resolve(project, defaults, config)
	if err != nil {
		t.Fatal(err)
	}
	want := []TmpfsMount{
		{Target: environments[0], Mode: projectenv.DefaultTmpfsMode},
		{Target: environments[1], Mode: projectenv.DefaultTmpfsMode},
	}
	if !reflect.DeepEqual(resolution.Plan.TmpfsMounts, want) {
		t.Fatalf("TmpfsMounts = %#v, want %#v", resolution.Plan.TmpfsMounts, want)
	}

	config.Common.UseHostPythonVenv = true
	resolution, err = Resolve(project, defaults, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Plan.TmpfsMounts) != 0 {
		t.Fatalf("TmpfsMounts = %#v, want host environments exposed", resolution.Plan.TmpfsMounts)
	}
}

func TestResolveTmpfsMountsRejectsTargetsOutsideProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root, CommonGitDir: gitDir}
	defaults := resolvedConfig(project, false)
	config := defaults
	config.Common.TmpfsMounts = []projectenv.TmpfsMountConfig{{
		Target: filepath.Join(filepath.Dir(root), ".venv"), Mode: projectenv.DefaultTmpfsMode,
	}}
	if _, err := Resolve(project, defaults, config); err == nil || !strings.Contains(err.Error(), "inside working-tree root") {
		t.Fatalf("Resolve() error = %v, want out-of-project tmpfs rejection", err)
	}
}

func TestResolveDependencyCachesRejectsProjectAndHomeOverlaps(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(base, "project")
	gitDir := filepath.Join(root, ".git")
	for _, path := range []string{home, root, gitDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	project := gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root, CommonGitDir: gitDir}
	defaults := resolvedConfig(project, false)
	for _, source := range []string{home, root} {
		config := defaults
		config.Common.DependencyCaches = []projectenv.DependencyCacheConfig{{Kind: projectenv.DependencyCacheGoBuild, Source: source}}
		if _, err := ResolveWithHostHome(project, defaults, config, home); err == nil || !strings.Contains(err.Error(), "dependency cache") {
			t.Fatalf("ResolveWithHostHome(%q) error = %v", source, err)
		}
	}
}

func TestDependencyCacheEnvironmentKeyIsExplicit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind projectenv.DependencyCacheKind
		want string
	}{
		{projectenv.DependencyCacheGoBuild, "GOCACHE"},
		{projectenv.DependencyCacheGoModules, "GOMODCACHE"},
		{projectenv.DependencyCacheUV, "UV_CACHE_DIR"},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			got, err := (DependencyCache{Kind: test.kind}).EnvironmentKey()
			if err != nil || got != test.want {
				t.Fatalf("EnvironmentKey() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
	if _, err := (DependencyCache{Kind: "future"}).EnvironmentKey(); err == nil {
		t.Fatal("EnvironmentKey() accepted unknown kind")
	}
}

func TestNormalizeLogicalMountsOrdersParentBeforeInterleavedChild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := BindMount{Source: filepath.Join(root, "parent"), Target: "/container/parent"}
	child := BindMount{Source: filepath.Join(root, "parent", "child"), Target: "/container/parent/child"}
	unrelated := BindMount{Source: filepath.Join(root, "unrelated"), Target: "/container/unrelated"}

	mounts, provenance, err := normalizeLogicalMounts([]logicalMount{
		{mount: child, role: "child"},
		{mount: unrelated, role: "unrelated"},
		{mount: parent, role: "parent"},
	})
	if err != nil {
		t.Fatalf("normalizeLogicalMounts() error = %v", err)
	}
	if want := []BindMount{unrelated, parent}; !reflect.DeepEqual(mounts, want) {
		t.Fatalf("mounts = %#v, want %#v", mounts, want)
	}
	if want := []projectenv.MountRole{"parent", "child"}; !reflect.DeepEqual(provenance[1].Roles, want) {
		t.Fatalf("parent roles = %#v, want %#v", provenance[1].Roles, want)
	}
}

func TestResolveRejectsOmittedRequiredRoleAndReportsOptionalDeletion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(gitConfig, []byte("[user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{
		RequestedDir: root,
		WorktreeRoot: root,
		PrimaryRoot:  root,
		CommonGitDir: gitDir,
	}
	defaults := resolvedConfig(project, false)
	defaults.Common.Mounts = append([]projectenv.MountConfig{{
		Role:     projectenv.RoleHostGitConfig,
		Source:   gitConfig,
		Target:   filepath.Join(root, "container-gitconfig"),
		ReadOnly: true,
	}}, defaults.Common.Mounts...)

	withoutOptional := defaults
	withoutOptional.Common.Mounts = withoutOptional.Common.Mounts[1:]
	resolution, err := Resolve(project, defaults, withoutOptional)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Degradation{{Role: projectenv.RoleHostGitConfig}}; !reflect.DeepEqual(resolution.Degradations, want) {
		t.Fatalf("Degradations = %#v, want %#v", resolution.Degradations, want)
	}

	withoutRequired := withoutOptional
	withoutRequired.Common.Mounts = withoutRequired.Common.Mounts[1:]
	_, err = Resolve(project, defaults, withoutRequired)
	if err == nil || !strings.Contains(err.Error(), "required mount role") {
		t.Fatalf("Resolve() error = %v, want required-role rejection", err)
	}
}

func resolvedConfig(project gitproject.Project, linked bool) projectenv.ProjectConfig {
	return projectenv.ProjectConfig{
		Common: projectenv.CommonConfig{
			Image: "test:image",
			Mounts: []projectenv.MountConfig{
				{
					Role:     projectenv.RolePrimaryCheckout,
					Source:   project.PrimaryRoot,
					Target:   project.PrimaryRoot,
					ReadOnly: linked,
				},
				{
					Role:   projectenv.RoleCommonGitDir,
					Source: project.CommonGitDir,
					Target: project.CommonGitDir,
				},
				{
					Role:   projectenv.RoleWorktree,
					Source: project.WorktreeRoot,
					Target: project.WorktreeRoot,
				},
				{
					Role:   projectenv.RoleHostMCPChannel,
					Source: projectenv.HostMCPChannelSource,
					Target: projectenv.HostMCPChannelTarget,
				},
			},
		},
	}
}
