// Package projectenv defines the project-owned Docker build context used by the launcher.
package projectenv

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DockerfileSampleContent is inactive until the user renames Dockerfile.sample to Dockerfile.
//
//go:embed Dockerfile.sample
var DockerfileSampleContent string

const (
	Directory            = ".agents-safe"
	DockerfileName       = "Dockerfile"
	DockerfileSampleName = "Dockerfile.sample"
)

// Discover returns an empty path when the project has no .agents-safe/Dockerfile. The project root must be
// canonical because it becomes part of the launch plan's trusted filesystem boundary.
func Discover(projectRoot string) (string, error) {
	if err := validateProjectRoot(projectRoot); err != nil {
		return "", err
	}
	contextPath := filepath.Join(projectRoot, Directory)
	info, err := os.Lstat(contextPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect project environment %q: %w", contextPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("project environment %q is a symlink", contextPath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project environment %q is not a directory", contextPath)
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

// CreateSample creates an inactive project-environment template without overwriting existing content.
func CreateSample(projectRoot string) (string, error) {
	if err := validateProjectRoot(projectRoot); err != nil {
		return "", err
	}

	contextPath := filepath.Join(projectRoot, Directory)
	info, err := os.Lstat(contextPath)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			return "", fmt.Errorf("create project environment %q: %w", contextPath, err)
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect project environment %q: %w", contextPath, err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("project environment %q is a symlink", contextPath)
	} else if !info.IsDir() {
		return "", fmt.Errorf("project environment %q is not a directory", contextPath)
	}

	samplePath := filepath.Join(contextPath, DockerfileSampleName)
	file, err := os.OpenFile(samplePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("project environment sample %q already exists", samplePath)
	}
	if err != nil {
		return "", fmt.Errorf("create project environment sample %q: %w", samplePath, err)
	}
	if _, err := file.WriteString(DockerfileSampleContent); err != nil {
		_ = file.Close()
		_ = os.Remove(samplePath)
		return "", fmt.Errorf("write project environment sample %q: %w", samplePath, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(samplePath)
		return "", fmt.Errorf("close project environment sample %q: %w", samplePath, err)
	}

	return samplePath, nil
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
