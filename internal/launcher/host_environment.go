package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

func (docker *DockerLauncher) validateConfiguration() error {
	if docker == nil {
		return errors.New("Docker launcher is nil")
	}
	if docker.DockerBinary == "" {
		return errors.New("Docker binary is empty")
	}
	if docker.HostOS != "linux" {
		return fmt.Errorf("unsupported host OS %q: the MVP requires Linux", docker.HostOS)
	}
	if docker.CommandRunner == nil {
		return errors.New("Docker command runner is nil")
	}
	if docker.HostUID < 0 || docker.HostGID < 0 {
		return fmt.Errorf("invalid host identity %d:%d", docker.HostUID, docker.HostGID)
	}
	if err := validateAccountName("host user", docker.HostUser); err != nil {
		return err
	}
	if err := validateAccountName("host group", docker.HostGroup); err != nil {
		return err
	}
	if docker.HostHome == "/" {
		return errors.New("host home directory cannot be the filesystem root")
	}
	if err := launchplan.ValidateMountPath("host home directory", docker.HostHome); err != nil {
		return err
	}
	if docker.HostGitConfig != "" {
		if err := launchplan.ValidateMountPath("host Git config", docker.HostGitConfig); err != nil {
			return err
		}
	}
	return nil
}

func discoverHostGitConfig(hostHome string) (string, error) {
	path, err := filepath.Abs(filepath.Join(hostHome, ".gitconfig"))
	if err != nil {
		return "", fmt.Errorf("resolve host Git config path: %w", err)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect host Git config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("host Git config %q is not a regular file", path)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("canonicalize host Git config %q: %w", path, err)
	}
	return filepath.Clean(canonical), nil
}

func validateAccountName(label string, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is empty", label)
	}
	for index, character := range name {
		first := index == 0
		last := index == len(name)-1
		allowed := character >= 'a' && character <= 'z' ||
			!first && character >= '0' && character <= '9' ||
			character == '_' ||
			!first && character == '-' ||
			last && character == '$'
		if !allowed {
			return fmt.Errorf("%s name %q is unsupported", label, name)
		}
	}
	return nil
}

func validateSessionName(name string) error {
	if name == "" {
		return errors.New("session name is empty")
	}
	for index, character := range name {
		allowed := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '_' || character == '.' || character == '-')
		if !allowed {
			return fmt.Errorf("session name %q contains unsupported characters", name)
		}
	}
	return nil
}
