package launchplan

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestBuildRegularCheckout(t *testing.T) {
	t.Parallel()

	project := gitproject.Project{
		RequestedDir: "/sources/project/nested",
		WorktreeRoot: "/sources/project",
		GitDir:       "/sources/project/.git",
		CommonGitDir: "/sources/project/.git",
		PrimaryRoot:  "/sources/project",
	}

	plan, err := Build(project)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if plan.WorkingDir != project.RequestedDir {
		t.Errorf("WorkingDir = %q, want %q", plan.WorkingDir, project.RequestedDir)
	}
	if plan.ProjectRoot != project.WorktreeRoot {
		t.Errorf("ProjectRoot = %q, want %q", plan.ProjectRoot, project.WorktreeRoot)
	}
	wantMounts := []BindMount{{Source: "/sources/project", Target: "/sources/project"}}
	if !reflect.DeepEqual(plan.Mounts, wantMounts) {
		t.Errorf("Mounts = %#v, want %#v", plan.Mounts, wantMounts)
	}
}

func TestBuildLinkedWorktree(t *testing.T) {
	t.Parallel()

	project := gitproject.Project{
		RequestedDir: "/sources/feature worktree/nested",
		WorktreeRoot: "/sources/feature worktree",
		GitDir:       "/sources/primary/.git/worktrees/feature-worktree",
		CommonGitDir: "/sources/primary/.git",
		PrimaryRoot:  "/sources/primary",
		Linked:       true,
	}

	plan, err := Build(project)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if plan.ProjectRoot != project.WorktreeRoot {
		t.Errorf("ProjectRoot = %q, want %q", plan.ProjectRoot, project.WorktreeRoot)
	}

	wantMounts := []BindMount{
		{Source: "/sources/primary", Target: "/sources/primary", ReadOnly: true},
		{Source: "/sources/primary/.git", Target: "/sources/primary/.git"},
		{Source: "/sources/feature worktree", Target: "/sources/feature worktree"},
	}
	if !reflect.DeepEqual(plan.Mounts, wantMounts) {
		t.Errorf("Mounts = %#v, want %#v", plan.Mounts, wantMounts)
	}
}

func TestBuildRejectsWorkingDirectoryOutsideWorktree(t *testing.T) {
	t.Parallel()

	_, err := Build(gitproject.Project{
		RequestedDir: "/sources/other",
		WorktreeRoot: "/sources/project",
		PrimaryRoot:  "/sources/project",
	})
	if err == nil {
		t.Fatal("Build() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "outside working-tree root") {
		t.Fatalf("Build() error = %q, want outside working-tree root", err)
	}
}

func TestNormalizeMountsRemovesExactDuplicates(t *testing.T) {
	t.Parallel()

	mount := BindMount{Source: "/sources/project", Target: "/sources/project"}
	got, err := normalizeMounts([]BindMount{mount, mount})
	if err != nil {
		t.Fatalf("normalizeMounts() error = %v", err)
	}
	if !reflect.DeepEqual(got, []BindMount{mount}) {
		t.Errorf("normalizeMounts() = %#v, want one mount", got)
	}
}

func TestNormalizeMountsRejectsConflictingTargets(t *testing.T) {
	t.Parallel()

	_, err := normalizeMounts([]BindMount{
		{Source: "/sources/one", Target: "/workspace"},
		{Source: "/sources/two", Target: "/workspace"},
	})
	if err == nil {
		t.Fatal("normalizeMounts() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "conflicting mounts") {
		t.Fatalf("normalizeMounts() error = %q, want conflicting mounts", err)
	}
}

func TestBuildRejectsUnsafeMountPath(t *testing.T) {
	t.Parallel()

	_, err := Build(gitproject.Project{
		RequestedDir: "/sources/project,unsafe",
		WorktreeRoot: "/sources/project,unsafe",
		PrimaryRoot:  "/sources/project,unsafe",
	})
	if err == nil {
		t.Fatal("Build() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "cannot be represented safely") {
		t.Fatalf("Build() error = %q, want unsafe --mount error", err)
	}
}

func TestBuildAddsConfiguredMount(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	external := t.TempDir()
	writeMountConfig(t, root, external)

	plan, err := Build(gitproject.Project{
		RequestedDir: root,
		WorktreeRoot: root,
		PrimaryRoot:  root,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []BindMount{
		{Source: root, Target: root},
		{Source: external, Target: external},
	}
	if !reflect.DeepEqual(plan.Mounts, want) {
		t.Fatalf("Mounts = %#v, want %#v", plan.Mounts, want)
	}
}

func TestBuildRejectsConfiguredMountOverlap(t *testing.T) {
	t.Parallel()
	t.Run("managed project", func(t *testing.T) {
		root := t.TempDir()
		writeMountConfig(t, root, root)
		_, err := Build(gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root})
		if err == nil || !strings.Contains(err.Error(), "overlaps mount") {
			t.Fatalf("Build() error = %v, want overlap rejection", err)
		}
	})
	t.Run("configured parent", func(t *testing.T) {
		root := t.TempDir()
		parent := t.TempDir()
		child := filepath.Join(parent, "child")
		if err := os.Mkdir(child, 0o755); err != nil {
			t.Fatal(err)
		}
		writeMountConfig(t, root, parent, child)
		_, err := Build(gitproject.Project{RequestedDir: root, WorktreeRoot: root, PrimaryRoot: root})
		if err == nil || !strings.Contains(err.Error(), "overlaps mount") {
			t.Fatalf("Build() error = %v, want overlap rejection", err)
		}
	})
}

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
	wantRoles := []string{projectenv.RolePrimaryCheckout, projectenv.RoleWorktree, projectenv.RoleCommonGitDir}
	if !reflect.DeepEqual(resolution.Plan.Provenance[0].Roles, wantRoles) {
		t.Errorf("roles = %#v, want %#v", resolution.Plan.Provenance[0].Roles, wantRoles)
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

func writeMountConfig(t *testing.T, root string, mounts ...string) {
	t.Helper()
	contextPath := filepath.Join(root, projectenv.Directory)
	if err := os.Mkdir(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "mounts = ["
	for index, mount := range mounts {
		if index != 0 {
			content += ", "
		}
		content += fmt.Sprintf("%q", mount)
	}
	content += "]\n"
	if err := os.WriteFile(filepath.Join(contextPath, projectenv.ConfigName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
