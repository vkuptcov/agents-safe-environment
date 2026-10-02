package launchplan

import (
	"regexp"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const testHostUID = 1000

var (
	branchVolumePattern  = regexp.MustCompile(`^agents-safe-docker-[a-z0-9-]{15}-[a-z0-9-]{20}-[0-9a-f]{12}$`)
	projectVolumePattern = regexp.MustCompile(`^agents-safe-docker-[a-z0-9-]{15}-[0-9a-f]{12}$`)
)

func mustResolveStorage(t *testing.T, project gitproject.Project, mode projectenv.DockerStorageMode) DockerStorage {
	t.Helper()
	storage, err := resolveDockerStorage(project, mode, testHostUID)
	if err != nil {
		t.Fatalf("resolveDockerStorage(%q) error = %v", mode, err)
	}
	return storage
}

func longNameProject() gitproject.Project {
	return gitproject.Project{
		PrimaryRoot: "/repos/" + strings.Repeat("Project", 10),
		Branch:      strings.Repeat("feature/long-", 10),
	}
}

func TestDockerStorageVolumeNamesStayBounded(t *testing.T) {
	project := longNameProject()
	branch := mustResolveStorage(t, project, projectenv.DockerStorageBranch)
	if !branchVolumePattern.MatchString(branch.Volume) {
		t.Fatalf("branch volume = %q", branch.Volume)
	}
	scoped := mustResolveStorage(t, project, projectenv.DockerStorageProject)
	if !projectVolumePattern.MatchString(scoped.Volume) {
		t.Fatalf("project volume = %q", scoped.Volume)
	}
	shared := mustResolveStorage(t, project, projectenv.DockerStorageShared)
	if shared.Volume != "agents-safe-docker-shared" {
		t.Fatalf("shared volume = %q", shared.Volume)
	}
}

func TestDockerStorageRecordsOnlyTheIdentitiesItsScopeOwns(t *testing.T) {
	project := gitproject.Project{PrimaryRoot: "/work/api", Branch: "main"}
	branch := mustResolveStorage(t, project, projectenv.DockerStorageBranch)
	if branch.ProjectRoot != project.PrimaryRoot || branch.Branch != project.Branch {
		t.Fatalf("branch identities = %#v", branch)
	}
	scoped := mustResolveStorage(t, project, projectenv.DockerStorageProject)
	if scoped.ProjectRoot != project.PrimaryRoot || scoped.Branch != "" {
		t.Fatalf("project identities = %#v", scoped)
	}
	shared := mustResolveStorage(t, project, projectenv.DockerStorageShared)
	if shared.ProjectRoot != "" || shared.Branch != "" {
		t.Fatalf("host-wide storage claims an owner: %#v", shared)
	}
}

func TestDockerStorageSelectsVolumeByScope(t *testing.T) {
	project := longNameProject()
	branch := mustResolveStorage(t, project, projectenv.DockerStorageBranch)
	linked := project
	linked.WorktreeRoot = "/worktrees/feature"
	if got := mustResolveStorage(t, linked, projectenv.DockerStorageBranch); got.Volume != branch.Volume {
		t.Fatalf("linked worktree changed volume: %q", got.Volume)
	}
	otherBranch := project
	otherBranch.Branch += "different"
	if got := mustResolveStorage(t, otherBranch, projectenv.DockerStorageBranch); got.Volume == branch.Volume {
		t.Fatalf("truncated branches collide: %q", got.Volume)
	}
	scoped := mustResolveStorage(t, project, projectenv.DockerStorageProject)
	if got := mustResolveStorage(t, otherBranch, projectenv.DockerStorageProject); got.Volume != scoped.Volume {
		t.Fatalf("branch changed project volume: %q", got.Volume)
	}
}

func TestDockerStorageIsolatesProjectsAndUsers(t *testing.T) {
	project := gitproject.Project{PrimaryRoot: "/work/api", Branch: "main"}
	otherProject := project
	otherProject.PrimaryRoot = "/clients/api"
	for _, mode := range []projectenv.DockerStorageMode{projectenv.DockerStorageBranch, projectenv.DockerStorageProject} {
		t.Run(string(mode), func(t *testing.T) {
			original := mustResolveStorage(t, project, mode)
			if got := mustResolveStorage(t, otherProject, mode); got.Volume == original.Volume {
				t.Fatalf("same basename at %q shares storage %q", otherProject.PrimaryRoot, got.Volume)
			}
			otherUser, err := resolveDockerStorage(project, mode, testHostUID+1)
			if err != nil || otherUser.Volume == original.Volume {
				t.Fatalf("another host user shares storage %q: %v", otherUser.Volume, err)
			}
		})
	}
	shared := mustResolveStorage(t, project, projectenv.DockerStorageShared)
	otherUserShared, err := resolveDockerStorage(otherProject, projectenv.DockerStorageShared, testHostUID+1)
	if err != nil || otherUserShared.Volume != shared.Volume {
		t.Fatalf("shared scope must be host-wide: %q, %v", otherUserShared.Volume, err)
	}
}

func TestDockerStorageNormalizationKeepsDistinctNames(t *testing.T) {
	project := gitproject.Project{PrimaryRoot: "/work/api"}
	for _, branches := range [][2]string{{"foo/bar", "foo-bar"}, {"Ä", "Ö"}} {
		project.Branch = branches[0]
		first := mustResolveStorage(t, project, projectenv.DockerStorageBranch)
		project.Branch = branches[1]
		second := mustResolveStorage(t, project, projectenv.DockerStorageBranch)
		if first.Volume == second.Volume {
			t.Fatalf("%q and %q both resolve %q", branches[0], branches[1], first.Volume)
		}
	}
}

func TestDockerStorageRejectsUnusableSelection(t *testing.T) {
	if _, err := resolveDockerStorage(gitproject.Project{PrimaryRoot: "/work/api"}, "invalid", testHostUID); err == nil {
		t.Fatal("invalid scope accepted")
	}
	unborn := gitproject.Project{PrimaryRoot: "/work/api"}
	if _, err := resolveDockerStorage(unborn, projectenv.DockerStorageBranch, testHostUID); err == nil {
		t.Fatal("branch scope accepted without a branch identity")
	}
}
