package projectenv

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed Dockerfile.sample
var dockerfileSampleContent string

//go:embed config.toml
var configSampleContent string

var localIgnoreRules = []string{
	"/.agents-safe/Dockerfile.sample",
	"/.agents-safe/config.toml",
}

// Initialize creates missing local project-environment files and preserves existing content.
func Initialize(projectRoot string) (string, error) {
	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			return "", fmt.Errorf("create project environment %q: %w", contextPath, err)
		}
	}

	resources := []struct {
		name    string
		content string
	}{
		{name: DockerfileSampleName, content: dockerfileSampleContent},
		{name: ConfigName, content: configSampleContent},
	}
	for _, resource := range resources {
		if err := createFileIfMissing(filepath.Join(contextPath, resource.name), resource.content); err != nil {
			return "", err
		}
	}
	if err := updateGitignore(filepath.Join(projectRoot, ".gitignore")); err != nil {
		return "", err
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

func updateGitignore(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		content := "# agents-safe local files\n" + strings.Join(localIgnoreRules, "\n") + "\n"
		return createFileIfMissing(path, content)
	}
	if err != nil {
		return fmt.Errorf("inspect project .gitignore %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("project .gitignore %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read project .gitignore %q: %w", path, err)
	}

	present := make(map[string]bool, len(localIgnoreRules))
	for _, line := range strings.Split(string(data), "\n") {
		present[strings.TrimSuffix(line, "\r")] = true
	}
	missing := make([]string, 0, len(localIgnoreRules))
	for _, rule := range localIgnoreRules {
		if !present[rule] {
			missing = append(missing, rule)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	addition := prefix + "# agents-safe local files\n" + strings.Join(missing, "\n") + "\n"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open project .gitignore %q: %w", path, err)
	}
	if _, err := file.WriteString(addition); err != nil {
		_ = file.Close()
		return fmt.Errorf("update project .gitignore %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close project .gitignore %q: %w", path, err)
	}
	return nil
}
