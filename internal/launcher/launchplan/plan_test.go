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
