package launchplan

import (
	"regexp"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
)

func TestDockerStorageNames(t *testing.T) {
	project := gitproject.Project{PrimaryRoot: "/repos/" + strings.Repeat("Project", 10), Branch: strings.Repeat("feature/long-", 10)}
	branch, err := dockerStorageVolume(project, "branch", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(branch) != 68 || !regexp.MustCompile(`^agents-safe-docker-[a-z0-9-]{15}-[a-z0-9-]{20}-[0-9a-f]{12}$`).MatchString(branch) {
		t.Fatalf("branch name = %q", branch)
	}
	other := project
	other.WorktreeRoot = "/worktrees/feature"
	if got, _ := dockerStorageVolume(other, "branch", 1000); got != branch {
		t.Fatal("worktree path changed volume")
	}
	other.Branch += "different"
	if got, _ := dockerStorageVolume(other, "branch", 1000); got == branch {
		t.Fatal("truncation collision")
	}
	scoped, err := dockerStorageVolume(project, "project", 1000)
	if err != nil || len(scoped) != 47 {
		t.Fatalf("project name = %q, %v", scoped, err)
	}
	if got, _ := dockerStorageVolume(other, "project", 1000); got != scoped {
		t.Fatal("branch changed project volume")
	}
	if got, _ := dockerStorageVolume(other, "shared", 1000); got != "agents-safe-docker-shared" {
		t.Fatalf("shared = %q", got)
	}
	if _, err := dockerStorageVolume(project, "invalid", 1000); err == nil {
		t.Fatal("invalid scope accepted")
	}
	for _, names := range [][2]string{{"foo/bar", "foo-bar"}, {"Ä", "Ö"}} {
		project.Branch = names[0]
		a, _ := dockerStorageVolume(project, "branch", 1000)
		project.Branch = names[1]
		b, _ := dockerStorageVolume(project, "branch", 1000)
		if a == b {
			t.Fatalf("normalization collision: %q", a)
		}
	}
}

func TestDockerStorageIsolatesProjectsAndUsers(t *testing.T) {
	project := gitproject.Project{PrimaryRoot: "/work/api", Branch: "main"}
	for _, mode := range []string{"branch", "project"} {
		original, _ := dockerStorageVolume(project, mode, 1000)
		other := project
		other.PrimaryRoot = "/clients/api"
		if got, _ := dockerStorageVolume(other, mode, 1000); got == original {
			t.Fatalf("%s shares unrelated projects", mode)
		}
		if got, _ := dockerStorageVolume(project, mode, 1001); got == original {
			t.Fatalf("%s shares users", mode)
		}
	}
	a, _ := dockerStorageVolume(project, "shared", 1000)
	project.PrimaryRoot = "/other/api"
	b, _ := dockerStorageVolume(project, "shared", 1001)
	if a != b {
		t.Fatal("shared scope must be host-wide")
	}
}
