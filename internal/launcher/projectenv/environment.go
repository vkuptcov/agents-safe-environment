// Package projectenv defines the project-owned Docker build context used by the launcher.
package projectenv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	Directory      = ".agents-safe"
	DockerfileName = "Dockerfile"
)

// Definition is the fixed project build context selected by discovery.
type Definition struct {
	ContextPath    string
	DockerfilePath string
}

// Discover returns nil when the project has no .agents-safe/Dockerfile. The project root must be
// canonical because it becomes part of the launch plan's trusted filesystem boundary.
func Discover(projectRoot string) (*Definition, error) {
	if !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot {
		return nil, fmt.Errorf("project root %q is not canonical", projectRoot)
	}
	contextPath := filepath.Join(projectRoot, Directory)
	info, err := os.Lstat(contextPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect project environment %q: %w", contextPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("project environment %q is a symlink", contextPath)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project environment %q is not a directory", contextPath)
	}

	dockerfilePath := filepath.Join(contextPath, DockerfileName)
	dockerfileInfo, err := os.Lstat(dockerfilePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect project Dockerfile %q: %w", dockerfilePath, err)
	}
	if !dockerfileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("project Dockerfile %q is not a regular file", dockerfilePath)
	}

	return &Definition{ContextPath: contextPath, DockerfilePath: dockerfilePath}, nil
}

// LocalImageName creates the stable Docker tag rebuilt for one project whenever a session is created.
func LocalImageName(projectKey string) (string, error) {
	if strings.TrimSpace(projectKey) == "" || strings.ContainsAny(projectKey, " \t\r\n/@:") {
		return "", fmt.Errorf("invalid project key %q", projectKey)
	}
	return "codex-safe-project-" + projectKey + ":local", nil
}
