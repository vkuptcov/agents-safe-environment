package projectenv

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed Dockerfile.sample
var dockerfileSampleContent string

const localIgnoreContent = "*\n!.gitignore\n!Dockerfile\n"

// Initialize creates missing local project-environment files and preserves existing content.
func Initialize(projectRoot string, config ProjectConfig) (string, error) {
	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			return "", fmt.Errorf("create project environment %q: %w", contextPath, err)
		}
	}

	var encodedConfig bytes.Buffer
	if err := Encode(config, &encodedConfig); err != nil {
		return "", fmt.Errorf("encode initialization config: %w", err)
	}
	resources := []struct {
		name    string
		content string
	}{
		{name: DockerfileSampleName, content: dockerfileSampleContent},
		{name: ConfigName, content: encodedConfig.String()},
		{name: ".gitignore", content: localIgnoreContent},
	}
	for _, resource := range resources {
		if err := createFileIfMissing(filepath.Join(contextPath, resource.name), resource.content); err != nil {
			return "", err
		}
	}
	return contextPath, nil
}

func createFileIfMissing(path string, content string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("local project-environment file %q is not a regular file", path)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect local project-environment file %q: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create local project-environment file %q: %w", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write local project-environment file %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close local project-environment file %q: %w", path, err)
	}
	return nil
}
