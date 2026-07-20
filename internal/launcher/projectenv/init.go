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
	path, _, err := InitializeLazy(projectRoot, func() (ProjectConfig, error) { return config, nil })
	return path, err
}

// InitializeLazy calls config only when config.toml is absent, so init-only host discovery cannot rewrite or even
// rediscover an existing snapshot.
func InitializeLazy(projectRoot string, config func() (ProjectConfig, error)) (string, bool, error) {
	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil {
		return "", false, err
	}
	if !exists {
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			return "", false, fmt.Errorf("create project environment %q: %w", contextPath, err)
		}
	}

	configPath := filepath.Join(contextPath, ConfigName)
	info, err := os.Lstat(configPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", false, fmt.Errorf("inspect local project-environment file %q: %w", configPath, err)
	}
	if err == nil && !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("local project-environment file %q is not a regular file", configPath)
	}
	createdConfig := errors.Is(err, fs.ErrNotExist)
	encodedConfig := ""
	if createdConfig {
		resolved, resolveErr := config()
		if resolveErr != nil {
			return "", false, resolveErr
		}
		var buffer bytes.Buffer
		if encodeErr := Encode(resolved, &buffer); encodeErr != nil {
			return "", false, fmt.Errorf("encode initialization config: %w", encodeErr)
		}
		encodedConfig = buffer.String()
	}
	resources := []struct {
		name    string
		content string
	}{
		{name: DockerfileSampleName, content: dockerfileSampleContent},
		{name: ConfigName, content: encodedConfig},
		{name: ".gitignore", content: localIgnoreContent},
	}
	for _, resource := range resources {
		if err := createFileIfMissing(filepath.Join(contextPath, resource.name), resource.content); err != nil {
			return "", false, err
		}
	}
	return contextPath, createdConfig, nil
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
