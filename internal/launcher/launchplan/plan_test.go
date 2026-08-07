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
	registry := filepath.Join(gitDir, "worktrees")
	wantMounts := []BindMount{
		{Source: root, Target: root},
		{Source: registry, Target: registry, ReadOnly: true},
	}
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
	if resolution.Plan.WorktreeRegistryDir != registry {
		t.Fatalf("WorktreeRegistryDir = %q, want %q", resolution.Plan.WorktreeRegistryDir, registry)
	}
	if _, err := os.Lstat(registry); !os.IsNotExist(err) {
		t.Fatalf("Resolve materialized worktree registry %q: %v", registry, err)
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
	registry := filepath.Join(commonGit, "worktrees")
	gitDir := filepath.Join(registry, "feature")
	for _, path := range []string{primary, worktree, commonGit, gitDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	project := gitproject.Project{
		RequestedDir: worktree,
		WorktreeRoot: worktree,
		PrimaryRoot:  primary,
		GitDir:       gitDir,
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
		{Source: registry, Target: registry, ReadOnly: true},
		{Source: gitDir, Target: gitDir},
	}
	if len(resolution.Plan.Mounts) != len(want) {
		t.Fatalf("Mounts = %#v, want %d mounts", resolution.Plan.Mounts, len(want))
	}
	for _, mount := range want {
		if findMountIndex(resolution.Plan.Mounts, mount) < 0 {
			t.Fatalf("Mounts = %#v, missing %#v", resolution.Plan.Mounts, mount)
		}
	}
	chain := []BindMount{want[0], want[1], want[3], want[4]}
	for index := 1; index < len(chain); index++ {
		if findMountIndex(resolution.Plan.Mounts, chain[index-1]) >= findMountIndex(resolution.Plan.Mounts, chain[index]) {
			t.Fatalf("Mounts = %#v, want parent-before-child chain %#v", resolution.Plan.Mounts, chain)
		}
	}
}

func TestResolveRejectsUnexpectedLinkedGitDirTopology(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	worktree := filepath.Join(base, "worktree")
	commonGit := filepath.Join(primary, ".git")
	registry := filepath.Join(commonGit, "worktrees")
	for _, path := range []string{worktree, registry} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		gitDir string
		setup  func(string) error
	}{
		{
			name:   "nested child",
			gitDir: filepath.Join(registry, "group", "feature"),
			setup:  func(path string) error { return os.MkdirAll(path, 0o755) },
		},
		{
			name:   "symlink child",
			gitDir: filepath.Join(registry, "feature-link"),
			setup: func(path string) error {
				realGitDir := filepath.Join(base, "real-feature-git-dir")
				if err := os.Mkdir(realGitDir, 0o755); err != nil {
					return err
				}
				return os.Symlink(realGitDir, path)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.setup(test.gitDir); err != nil {
				t.Fatal(err)
			}
			project := gitproject.Project{
				RequestedDir: worktree,
				WorktreeRoot: worktree,
				GitDir:       test.gitDir,
				CommonGitDir: commonGit,
				PrimaryRoot:  primary,
				Linked:       true,
			}
			defaults := resolvedConfig(project, true)
			if _, err := Resolve(project, defaults, defaults); err == nil ||
				!strings.Contains(err.Error(), "canonical direct child") {
				t.Fatalf("Resolve() error = %v, want linked GitDir topology rejection", err)
			}
		})
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
		{Target: environments[0], Mode: projectenv.DefaultTmpfsMode, Owned: true},
		{Target: environments[1], Mode: projectenv.DefaultTmpfsMode, Owned: true},
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

func TestResolvePythonProjectReservesRootVenvBeforeItExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveTmpfsMounts(root, nil, false, filepath.Join(root, ".git", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	want := []TmpfsMount{{
		Target:       filepath.Join(root, ".venv"),
		Mode:         projectenv.DefaultTmpfsMode,
		CreateTarget: true,
		Owned:        true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveTmpfsMounts() = %#v, want %#v", got, want)
	}
	if _, err := os.Lstat(want[0].Target); !os.IsNotExist(err) {
		t.Fatalf("Resolve materialized %q before cold-container creation: %v", want[0].Target, err)
	}

	got, err = resolveTmpfsMounts(root, nil, true, filepath.Join(root, ".git", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("resolveTmpfsMounts() = %#v, want host environment policy to skip reservation", got)
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

func TestResolveProtectsWorktreeRegistryFromConfiguredMountBypasses(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "project")
	gitDir := filepath.Join(root, ".git")
	registry := filepath.Join(gitDir, "worktrees")
	external := filepath.Join(base, "external")
	safePhysical := filepath.Join(base, "safe-physical")
	safeAlias := filepath.Join(base, "safe-alias")
	registryAlias := filepath.Join(base, "registry-alias")
	for _, path := range []string{registry, external, safePhysical} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(safePhysical, safeAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(registry, registryAlias); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root, CommonGitDir: gitDir}
	defaults := resolvedConfig(project, false)

	t.Run("preserves safe source spelling", func(t *testing.T) {
		config := defaults
		config.Common.Mounts = append(append([]projectenv.MountConfig(nil), defaults.Common.Mounts...), projectenv.MountConfig{
			Role: projectenv.RoleAdditional, Source: safeAlias, Target: external,
		})
		resolution, err := Resolve(project, defaults, config)
		if err != nil {
			t.Fatal(err)
		}
		if findMountIndex(resolution.Plan.Mounts, BindMount{Source: safeAlias, Target: external}) < 0 {
			t.Fatalf("Mounts = %#v, want configured symlink spelling preserved", resolution.Plan.Mounts)
		}
	})

	tests := []struct {
		name   string
		source string
		target string
	}{
		{name: "source symlink", source: registryAlias, target: external},
		{name: "target", source: external, target: filepath.Join(registry, "hidden")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := defaults
			config.Common.Mounts = append(append([]projectenv.MountConfig(nil), defaults.Common.Mounts...), projectenv.MountConfig{
				Role: projectenv.RoleAdditional, Source: test.source, Target: test.target,
			})
			if _, err := Resolve(project, defaults, config); err == nil ||
				!strings.Contains(err.Error(), "protected worktree registry") {
				t.Fatalf("Resolve() error = %v, want protected-registry overlap rejection", err)
			}
		})
	}
}

func TestResolveRejectsDependencyCacheAndTmpfsRegistryOverlaps(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(base, "project")
	gitDir := filepath.Join(root, ".git")
	registry := filepath.Join(gitDir, "worktrees")
	cacheAlias := filepath.Join(home, "cache-alias")
	for _, path := range []string{home, registry} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(registry, cacheAlias); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root, CommonGitDir: gitDir}
	defaults := resolvedConfig(project, false)

	cacheConfig := defaults
	cacheConfig.Common.DependencyCaches = []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheGoBuild, Source: cacheAlias,
	}}
	if _, err := ResolveWithHostHome(project, defaults, cacheConfig, home); err == nil ||
		!strings.Contains(err.Error(), "protected worktree registry") {
		t.Fatalf("ResolveWithHostHome() error = %v, want cache overlap rejection", err)
	}

	tmpfsConfig := defaults
	tmpfsConfig.Common.TmpfsMounts = []projectenv.TmpfsMountConfig{{
		Target: registry, Mode: projectenv.DefaultTmpfsMode,
	}}
	if _, err := Resolve(project, defaults, tmpfsConfig); err == nil ||
		!strings.Contains(err.Error(), "protected worktree registry") {
		t.Fatalf("Resolve() error = %v, want tmpfs overlap rejection", err)
	}
}

func TestResolveLegacyThreeRoleConfigReceivesDerivedGuard(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root, CommonGitDir: gitDir}
	defaults := resolvedConfig(project, false)
	legacy := defaults
	legacy.Common.Mounts = append([]projectenv.MountConfig(nil), defaults.Common.Mounts[:3]...)
	resolution, err := Resolve(project, defaults, legacy)
	if err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(gitDir, "worktrees")
	if findMountIndex(resolution.Plan.Mounts, BindMount{Source: registry, Target: registry, ReadOnly: true}) < 0 {
		t.Fatalf("Mounts = %#v, want derived registry guard", resolution.Plan.Mounts)
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

func TestNormalizeLogicalMountsUsesNearestEffectiveAncestor(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repo")
	chain := func(readOnly ...bool) []logicalMount {
		paths := []string{root, filepath.Join(root, ".git"), filepath.Join(root, ".git", "worktrees", "feature")}
		result := make([]logicalMount, 0, len(paths))
		for index, path := range paths {
			result = append(result, logicalMount{
				mount: BindMount{Source: path, Target: path, ReadOnly: readOnly[index]},
				role:  projectenv.MountRole(string(rune('a' + index))),
			})
		}
		return result
	}
	tests := []struct {
		name    string
		logical []logicalMount
		want    []BindMount
		wantErr bool
	}{
		{
			name:    "rw ro rw",
			logical: chain(false, true, false),
			want:    mountsFromLogical(chain(false, true, false)),
		},
		{
			name:    "ro rw rw",
			logical: chain(true, false, false),
			want:    mountsFromLogical(chain(true, false, false)[:2]),
		},
		{
			name:    "rw rw ro",
			logical: chain(false, false, true),
			want: []BindMount{
				{Source: root, Target: root},
				{Source: filepath.Join(root, ".git", "worktrees", "feature"), Target: filepath.Join(root, ".git", "worktrees", "feature"), ReadOnly: true},
			},
		},
		{
			name: "exact aliases",
			logical: []logicalMount{
				{mount: BindMount{Source: root, Target: root}, role: "first"},
				{mount: BindMount{Source: root, Target: root}, role: "second"},
			},
			want: []BindMount{{Source: root, Target: root}},
		},
		{
			name: "incompatible mapping",
			logical: []logicalMount{
				{mount: BindMount{Source: root, Target: "/container/repo"}},
				{mount: BindMount{Source: filepath.Join(root, ".git"), Target: "/container/metadata"}},
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mounts, _, err := normalizeLogicalMounts(test.logical)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeLogicalMounts() = %#v, want error", mounts)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mounts, test.want) {
				t.Fatalf("mounts = %#v, want %#v", mounts, test.want)
			}
		})
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

func mountsFromLogical(logical []logicalMount) []BindMount {
	result := make([]BindMount, 0, len(logical))
	for _, mount := range logical {
		result = append(result, mount.mount)
	}
	return result
}

func findMountIndex(mounts []BindMount, want BindMount) int {
	for index, mount := range mounts {
		if mount == want {
			return index
		}
	}
	return -1
}
