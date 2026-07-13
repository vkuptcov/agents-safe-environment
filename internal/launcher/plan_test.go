package launcher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
)

func TestBuildPlanRegularCheckout(t *testing.T) {
	t.Parallel()

	project := gitproject.Project{
		RequestedDir: "/sources/project/nested",
		WorktreeRoot: "/sources/project",
		GitDir:       "/sources/project/.git",
		CommonGitDir: "/sources/project/.git",
		PrimaryRoot:  "/sources/project",
	}

	plan, err := BuildPlan(project)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	if plan.WorkingDir != project.RequestedDir {
		t.Errorf("WorkingDir = %q, want %q", plan.WorkingDir, project.RequestedDir)
	}
	wantMounts := []Mount{{Source: "/sources/project", Target: "/sources/project"}}
	if !reflect.DeepEqual(plan.Mounts, wantMounts) {
		t.Errorf("Mounts = %#v, want %#v", plan.Mounts, wantMounts)
	}
}

func TestBuildPlanLinkedWorktree(t *testing.T) {
	t.Parallel()

	project := gitproject.Project{
		RequestedDir: "/sources/feature worktree/nested",
		WorktreeRoot: "/sources/feature worktree",
		GitDir:       "/sources/primary/.git/worktrees/feature-worktree",
		CommonGitDir: "/sources/primary/.git",
		PrimaryRoot:  "/sources/primary",
		Linked:       true,
	}

	plan, err := BuildPlan(project)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	wantMounts := []Mount{
		{Source: "/sources/primary", Target: "/sources/primary", ReadOnly: true},
		{Source: "/sources/primary/.git", Target: "/sources/primary/.git"},
		{Source: "/sources/feature worktree", Target: "/sources/feature worktree"},
	}
	if !reflect.DeepEqual(plan.Mounts, wantMounts) {
		t.Errorf("Mounts = %#v, want %#v", plan.Mounts, wantMounts)
	}
}

func TestBuildPlanRejectsWorkingDirectoryOutsideWorktree(t *testing.T) {
	t.Parallel()

	_, err := BuildPlan(gitproject.Project{
		RequestedDir: "/sources/other",
		WorktreeRoot: "/sources/project",
		PrimaryRoot:  "/sources/project",
	})
	if err == nil {
		t.Fatal("BuildPlan() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "outside working-tree root") {
		t.Fatalf("BuildPlan() error = %q, want outside working-tree root", err)
	}
}

func TestNormalizeMountsRemovesExactDuplicates(t *testing.T) {
	t.Parallel()

	mount := Mount{Source: "/sources/project", Target: "/sources/project"}
	got, err := normalizeMounts([]Mount{mount, mount})
	if err != nil {
		t.Fatalf("normalizeMounts() error = %v", err)
	}
	if !reflect.DeepEqual(got, []Mount{mount}) {
		t.Errorf("normalizeMounts() = %#v, want one mount", got)
	}
}

func TestNormalizeMountsRejectsConflictingTargets(t *testing.T) {
	t.Parallel()

	_, err := normalizeMounts([]Mount{
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

func TestBuildPlanRejectsUnsafeMountPath(t *testing.T) {
	t.Parallel()

	_, err := BuildPlan(gitproject.Project{
		RequestedDir: "/sources/project,unsafe",
		WorktreeRoot: "/sources/project,unsafe",
		PrimaryRoot:  "/sources/project,unsafe",
	})
	if err == nil {
		t.Fatal("BuildPlan() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "cannot be represented safely") {
		t.Fatalf("BuildPlan() error = %q, want unsafe --mount error", err)
	}
}
