// Package launchplan builds the validated filesystem contract passed from Git discovery to host
// container orchestration.
package launchplan

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
)

// BindMount describes one host path exposed to the Sysbox container through a Docker bind mount.
type BindMount struct {
	// Source is the canonical absolute path on the host.
	Source string
	// Target is the absolute path inside the container. It normally equals Source so Git and
	// nested Docker continue to see the same project paths as the host.
	Target string
	// ReadOnly prevents writes through this mount when true.
	ReadOnly bool
}

// Plan is the validated filesystem contract passed from Git-project discovery to the launcher.
// It identifies the managed worktree, preserves the caller's working directory, and restricts the host
// paths bind-mounted into the container.
type Plan struct {
	// ProjectRoot is the canonical root of the selected Git worktree. The launcher uses it as the
	// stable identity when it creates or reuses that worktree's managed container.
	ProjectRoot string
	// WorkingDir is the canonical caller directory within ProjectRoot. The launcher passes it to
	// Docker as the container's working directory.
	WorkingDir string
	// Mounts is the ordered, normalized set of host bind mounts for the container. Ordering
	// preserves the linked-worktree policy: a narrow writable Git mount follows its read-only parent.
	Mounts []BindMount
}

// Build converts a discovered Git project into a validated container launch plan.
func Build(project gitproject.Project) (Plan, error) {
	if err := validateWorkingDirectory(project.RequestedDir, project.WorktreeRoot); err != nil {
		return Plan{}, err
	}

	mounts := make([]BindMount, 0, 3)
	if project.Linked {
		mounts = append(mounts,
			BindMount{Source: project.PrimaryRoot, Target: project.PrimaryRoot, ReadOnly: true},
			BindMount{Source: project.CommonGitDir, Target: project.CommonGitDir},
			BindMount{Source: project.WorktreeRoot, Target: project.WorktreeRoot},
		)
	} else {
		mounts = append(mounts, BindMount{Source: project.WorktreeRoot, Target: project.WorktreeRoot})
	}

	normalized, err := normalizeMounts(mounts)
	if err != nil {
		return Plan{}, err
	}

	return Plan{
		ProjectRoot: project.WorktreeRoot,
		WorkingDir:  project.RequestedDir,
		Mounts:      normalized,
	}, nil
}

func validateWorkingDirectory(workingDir string, worktreeRoot string) error {
	if err := ValidateMountPath("working directory", workingDir); err != nil {
		return err
	}
	if err := ValidateMountPath("working-tree root", worktreeRoot); err != nil {
		return err
	}

	relative, err := filepath.Rel(worktreeRoot, workingDir)
	if err != nil {
		return fmt.Errorf("compare working directory to working-tree root: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("working directory %q is outside working-tree root %q", workingDir, worktreeRoot)
	}
	return nil
}

func normalizeMounts(mounts []BindMount) ([]BindMount, error) {
	normalized := make([]BindMount, 0, len(mounts))
	byTarget := make(map[string]BindMount, len(mounts))

	for _, mount := range mounts {
		if err := ValidateMountPath("mount source", mount.Source); err != nil {
			return nil, err
		}
		if err := ValidateMountPath("mount target", mount.Target); err != nil {
			return nil, err
		}

		existing, found := byTarget[mount.Target]
		if found {
			if existing == mount {
				continue
			}
			return nil, fmt.Errorf(
				"conflicting mounts for target %q: %+v and %+v",
				mount.Target,
				existing,
				mount,
			)
		}

		byTarget[mount.Target] = mount
		normalized = append(normalized, mount)
	}

	return normalized, nil
}

// ValidateMountPath verifies one canonical absolute host or container path used in a bind-mount contract.
func ValidateMountPath(label string, path string) error {
	if path == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s %q is not absolute", label, path)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("%s %q is not canonical", label, path)
	}
	if strings.ContainsAny(path, ",\x00\n\r") {
		return fmt.Errorf("%s %q cannot be represented safely with Docker --mount", label, path)
	}
	return nil
}
