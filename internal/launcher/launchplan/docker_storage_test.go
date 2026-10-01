package launchplan

import (
	"regexp"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
)

func TestDockerStorageNames(t *testing.T) {
	project := gitproject.Project{PrimaryRoot: "/repos/" + strings.Repeat("Project", 10), Branch: strings.Repeat("feature/long-", 10)}
	branch, err := dockerStorageVolume(project, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if len(branch) != 68 || !regexp.MustCompile(`^agents-safe-docker-[a-z0-9-]{15}-[a-z0-9-]{20}-[0-9a-f]{12}$`).MatchString(branch) {
		t.Fatalf("branch name = %q", branch)
	}
	other := project
	other.WorktreeRoot = "/worktrees/feature"
	if got, _ := dockerStorageVolume(other, "branch"); got != branch {
		t.Fatal("worktree path changed volume")
	}
	other.Branch += "different"
	if got, _ := dockerStorageVolume(other, "branch"); got == branch {
		t.Fatal("truncation collision")
	}
	scoped, err := dockerStorageVolume(project, "project")
	if err != nil || len(scoped) != 47 {
		t.Fatalf("project name = %q, %v", scoped, err)
	}
	if got, _ := dockerStorageVolume(other, "project"); got != scoped {
		t.Fatal("branch changed project volume")
	}
	if got, _ := dockerStorageVolume(other, "shared"); got != "agents-safe-docker-shared" {
		t.Fatalf("shared = %q", got)
	}
	if _, err := dockerStorageVolume(project, "invalid"); err == nil {
		t.Fatal("invalid scope accepted")
	}
	for _, names := range [][2]string{{"foo/bar", "foo-bar"}, {"Ä", "Ö"}} {
		project.Branch = names[0]
		a, _ := dockerStorageVolume(project, "branch")
		project.Branch = names[1]
		b, _ := dockerStorageVolume(project, "branch")
		if a == b {
			t.Fatalf("normalization collision: %q", a)
		}
	}
}
