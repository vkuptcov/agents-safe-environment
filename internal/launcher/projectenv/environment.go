// Package projectenv defines the project-owned Docker build context used by the launcher.
package projectenv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	Directory            = ".agents-safe"
	DockerfileName       = "Dockerfile"
	DockerfileSampleName = "Dockerfile.sample"
	ConfigName           = "config.toml"
)

// Discover returns an empty path when the project has no .agents-safe/Dockerfile. The project root must be
// canonical because it becomes part of the launch plan's trusted filesystem boundary.
func Discover(projectRoot string) (string, error) {
	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil || !exists {
		return "", err
	}

	dockerfilePath := filepath.Join(contextPath, DockerfileName)
	dockerfileInfo, err := os.Lstat(dockerfilePath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect project Dockerfile %q: %w", dockerfilePath, err)
	}
	if !dockerfileInfo.Mode().IsRegular() {
		return "", fmt.Errorf("project Dockerfile %q is not a regular file", dockerfilePath)
	}

	return contextPath, nil
}

func inspectContextDirectory(projectRoot string) (string, bool, error) {
	if err := validateProjectRoot(projectRoot); err != nil {
		return "", false, err
	}
	contextPath := filepath.Join(projectRoot, Directory)
	info, err := os.Lstat(contextPath)
	if errors.Is(err, fs.ErrNotExist) {
		return contextPath, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect project environment %q: %w", contextPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("project environment %q is a symlink", contextPath)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("project environment %q is not a directory", contextPath)
	}
	return contextPath, true, nil
}

func validateProjectRoot(projectRoot string) error {
	if !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot {
		return fmt.Errorf("project root %q is not canonical", projectRoot)
	}
	return nil
}

// LocalImageName creates the stable Docker tag rebuilt for one project whenever a session is created.
func LocalImageName(projectKey string) string {
	return "codex-safe-project-" + projectKey + ":local"
}
