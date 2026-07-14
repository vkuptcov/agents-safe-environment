package gitproject

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverRegularCheckoutFromNestedSymlink(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "regular repo ; $value")
	initRepository(t, root)

	nested := filepath.Join(root, "one", "two")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}

	link := filepath.Join(t.TempDir(), "project link")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatalf("create project symlink: %v", err)
	}

	project, err := Discover(context.Background(), link)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	wantRequested := mustCanonical(t, nested)
	wantRoot := mustCanonical(t, root)
	if project.RequestedDir != wantRequested {
		t.Errorf("RequestedDir = %q, want %q", project.RequestedDir, wantRequested)
	}
	if project.WorktreeRoot != wantRoot {
		t.Errorf("WorktreeRoot = %q, want %q", project.WorktreeRoot, wantRoot)
	}
	if project.PrimaryRoot != wantRoot {
		t.Errorf("PrimaryRoot = %q, want %q", project.PrimaryRoot, wantRoot)
	}
	if project.GitDir != filepath.Join(wantRoot, ".git") {
		t.Errorf("GitDir = %q, want %q", project.GitDir, filepath.Join(wantRoot, ".git"))
	}
	if project.CommonGitDir != filepath.Join(wantRoot, ".git") {
		t.Errorf("CommonGitDir = %q, want %q", project.CommonGitDir, filepath.Join(wantRoot, ".git"))
	}
	if project.Linked {
		t.Error("Linked = true, want false")
	}
}

func TestDiscoverLinkedWorktree(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	primary := filepath.Join(base, "primary repo")
	worktree := filepath.Join(base, "feature worktree ; $name")
	initRepository(t, primary)
	runGit(t, primary, "worktree", "add", "-b", "feature/test", worktree)

	nested := filepath.Join(worktree, "nested directory")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}

	project, err := Discover(context.Background(), nested)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	wantPrimary := mustCanonical(t, primary)
	wantWorktree := mustCanonical(t, worktree)
	wantCommon := filepath.Join(wantPrimary, ".git")
	if project.RequestedDir != mustCanonical(t, nested) {
		t.Errorf("RequestedDir = %q, want %q", project.RequestedDir, mustCanonical(t, nested))
	}
	if project.WorktreeRoot != wantWorktree {
		t.Errorf("WorktreeRoot = %q, want %q", project.WorktreeRoot, wantWorktree)
	}
	if project.PrimaryRoot != wantPrimary {
		t.Errorf("PrimaryRoot = %q, want %q", project.PrimaryRoot, wantPrimary)
	}
	if project.CommonGitDir != wantCommon {
		t.Errorf("CommonGitDir = %q, want %q", project.CommonGitDir, wantCommon)
	}
	if !strings.HasPrefix(project.GitDir, filepath.Join(wantCommon, "worktrees")+string(os.PathSeparator)) {
		t.Errorf("GitDir = %q, want it below %q", project.GitDir, filepath.Join(wantCommon, "worktrees"))
	}
	if !project.Linked {
		t.Error("Linked = false, want true")
	}
}

func TestDiscoverRejectsMissingAndNonGitDirectories(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"missing": filepath.Join(t.TempDir(), "missing"),
		"non-git": t.TempDir(),
	}
	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Discover(context.Background(), path)
			if err == nil {
				t.Fatal("Discover() error = nil, want an error")
			}
		})
	}
}

func TestDiscoverRejectsBareRepository(t *testing.T) {
	t.Parallel()

	bare := filepath.Join(t.TempDir(), "repository.git")
	runGit(t, "", "init", "--bare", bare)

	_, err := Discover(context.Background(), bare)
	if err == nil {
		t.Fatal("Discover() error = nil, want an error")
	}
}

func TestDiscoverRejectsSeparateGitDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	worktree := filepath.Join(base, "worktree")
	metadata := filepath.Join(base, "metadata")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	runGit(t, "", "init", "--separate-git-dir", metadata, worktree)

	_, err := Discover(context.Background(), worktree)
	if err == nil {
		t.Fatal("Discover() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "unsupported Git layout") {
		t.Fatalf("Discover() error = %q, want unsupported Git layout", err)
	}
}

func initRepository(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create repository directory: %v", err)
	}
	runGit(t, "", "init", "-b", "main", path)
	runGit(t, path, "config", "user.name", "Codex Safe Test")
	runGit(t, path, "config", "user.email", "codex-safe@example.invalid")

	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("baseline\n"), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	runGit(t, path, "add", "README.md")
	runGit(t, path, "commit", "-m", "baseline")
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()

	commandArgs := args
	if directory != "" {
		commandArgs = append([]string{"-C", directory}, args...)
	}
	command := exec.Command("git", commandArgs...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
}

func mustCanonical(t *testing.T, path string) string {
	t.Helper()

	canonical, err := canonicalPath(path)
	if err != nil {
		t.Fatalf("canonicalPath(%q): %v", path, err)
	}
	return canonical
}
