package gitproject

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Project contains the canonical host paths needed to mount a Git working tree.
type Project struct {
	RequestedDir string
	WorktreeRoot string
	GitDir       string
	CommonGitDir string
	PrimaryRoot  string
	Linked       bool
}

// Discover resolves a regular checkout or linked worktree rooted around requestedPath.
func Discover(ctx context.Context, requestedPath string) (Project, error) {
	requestedDir, err := canonicalDirectory(requestedPath)
	if err != nil {
		return Project{}, fmt.Errorf("resolve project path: %w", err)
	}

	inside, err := gitOutput(ctx, requestedDir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return Project{}, fmt.Errorf("discover Git working tree: %w", err)
	}
	if inside != "true" {
		return Project{}, fmt.Errorf("%q is not inside a Git working tree", requestedDir)
	}

	worktreeRoot, err := gitAbsolutePath(ctx, requestedDir, "--show-toplevel")
	if err != nil {
		return Project{}, fmt.Errorf("resolve working-tree root: %w", err)
	}
	gitDir, err := gitAbsolutePath(ctx, requestedDir, "--git-dir")
	if err != nil {
		return Project{}, fmt.Errorf("resolve Git directory: %w", err)
	}
	commonGitDir, err := gitAbsolutePath(ctx, requestedDir, "--git-common-dir")
	if err != nil {
		return Project{}, fmt.Errorf("resolve common Git directory: %w", err)
	}

	project := Project{
		RequestedDir: requestedDir,
		WorktreeRoot: worktreeRoot,
		GitDir:       gitDir,
		CommonGitDir: commonGitDir,
		Linked:       gitDir != commonGitDir,
	}

	if !project.Linked {
		expectedCommonDir := filepath.Join(worktreeRoot, ".git")
		if commonGitDir != expectedCommonDir {
			return Project{}, fmt.Errorf(
				"unsupported Git layout: common directory %q is not %q",
				commonGitDir,
				expectedCommonDir,
			)
		}
		project.PrimaryRoot = worktreeRoot
		return project, nil
	}

	primaryRoot, err := validatePrimaryCheckout(ctx, commonGitDir)
	if err != nil {
		return Project{}, err
	}
	project.PrimaryRoot = primaryRoot

	return project, nil
}

func validatePrimaryCheckout(ctx context.Context, commonGitDir string) (string, error) {
	if filepath.Base(commonGitDir) != ".git" {
		return "", fmt.Errorf(
			"unsupported linked worktree: common Git directory %q is not a primary checkout .git directory",
			commonGitDir,
		)
	}

	primaryRoot := filepath.Dir(commonGitDir)
	info, err := os.Stat(primaryRoot)
	if err != nil {
		return "", fmt.Errorf("inspect primary checkout %q: %w", primaryRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("primary checkout %q is not a directory", primaryRoot)
	}

	primaryWorktreeRoot, err := gitAbsolutePath(ctx, primaryRoot, "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("validate primary checkout %q: %w", primaryRoot, err)
	}
	if primaryWorktreeRoot != primaryRoot {
		return "", fmt.Errorf(
			"unsupported linked worktree: common Git directory belongs to %q, not %q",
			primaryWorktreeRoot,
			primaryRoot,
		)
	}

	primaryCommonDir, err := gitAbsolutePath(ctx, primaryRoot, "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("validate common Git directory for %q: %w", primaryRoot, err)
	}
	if primaryCommonDir != commonGitDir {
		return "", fmt.Errorf(
			"unsupported linked worktree: primary common Git directory is %q, expected %q",
			primaryCommonDir,
			commonGitDir,
		)
	}

	return primaryRoot, nil
}

func gitAbsolutePath(ctx context.Context, directory string, selector string) (string, error) {
	value, err := gitOutput(ctx, directory, "rev-parse", "--path-format=absolute", selector)
	if err != nil {
		return "", err
	}

	path, err := canonicalPath(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize Git path %q: %w", value, err)
	}
	return path, nil
}

func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}

	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("git %s returned an empty value", strings.Join(args, " "))
	}
	return value, nil
}

func canonicalDirectory(path string) (string, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", canonical)
	}
	return canonical, nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}
