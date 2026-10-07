package projectenv

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

//go:embed Dockerfile.sample
var dockerfileSampleContent string

const localFileLabel = "local project-environment file"

const localIgnoreContent = "*\n!.gitignore\n!Dockerfile\n!" + CommonConfigName + "\n"

// Initialization reports where the project environment lives and which config files this call created.
type Initialization struct {
	Path          string
	CreatedCommon bool
	CreatedConfig bool
}

// Initialize creates missing project-environment files from one config and preserves existing content.
func Initialize(projectRoot string, config ProjectConfig) (string, error) {
	provide := func() (ProjectConfig, error) { return config, nil }
	result, err := InitializeLazy(projectRoot, provide, provide)
	return result.Path, err
}

// InitializeLazy creates each missing file independently. common supplies the portable values written to the
// tracked common.toml; local supplies the host-specific lists written to config.toml. Each provider runs only
// when its file is absent, so init-only host discovery cannot rewrite or even rediscover an existing snapshot.
func InitializeLazy(projectRoot string, common, local func() (ProjectConfig, error)) (Initialization, error) {
	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil {
		return Initialization{}, err
	}
	if !exists {
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			return Initialization{}, fmt.Errorf("create project environment %q: %w", contextPath, err)
		}
	}

	commonContent, createdCommon, err := encodeIfMissing(filepath.Join(contextPath, CommonConfigName), common, EncodeCommon)
	if err != nil {
		return Initialization{}, err
	}
	localContent, createdConfig, err := encodeIfMissing(filepath.Join(contextPath, ConfigName), local, EncodeLocal)
	if err != nil {
		return Initialization{}, err
	}
	resources := []struct {
		name    string
		content string
	}{
		{name: DockerfileSampleName, content: dockerfileSampleContent},
		{name: CommonConfigName, content: commonContent},
		{name: ConfigName, content: localContent},
		{name: ".gitignore", content: localIgnoreContent},
	}
	for _, resource := range resources {
		if err := createFileIfMissing(filepath.Join(contextPath, resource.name), resource.content); err != nil {
			return Initialization{}, err
		}
	}
	return Initialization{Path: contextPath, CreatedCommon: createdCommon, CreatedConfig: createdConfig}, nil
}

// encodeIfMissing returns the encoded provider config when path is absent; an existing regular file is kept.
func encodeIfMissing(
	path string,
	provide func() (ProjectConfig, error),
	encode func(ProjectConfig, io.Writer) error,
) (string, bool, error) {
	exists, err := existingRegularFile(path, localFileLabel)
	if err != nil || exists {
		return "", false, err
	}
	config, err := provide()
	if err != nil {
		return "", false, err
	}
	var buffer bytes.Buffer
	if err := encode(config, &buffer); err != nil {
		return "", false, fmt.Errorf("encode initialization config %q: %w", path, err)
	}
	return buffer.String(), true, nil
}

func createFileIfMissing(path string, content string) error {
	exists, err := existingRegularFile(path, localFileLabel)
	if err != nil || exists {
		return err
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
