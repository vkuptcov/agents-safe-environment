package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

// HostEnvironmentInputs supplies the Docker-free host lookups used to resolve project configuration and initialization.
type HostEnvironmentInputs struct {
	UserHomeDir       func() (string, error)
	LookupEnv         func(string) (string, bool)
	DiscoverGitConfig func(string) (string, error)
}

// ResolveHostEnvironment resolves canonical optional host paths without contacting Docker.
func ResolveHostEnvironment() (HostEnvironment, error) {
	return resolveHostEnvironment(defaultHostEnvironmentInputs())
}

func defaultHostEnvironmentInputs() HostEnvironmentInputs {
	return HostEnvironmentInputs{
		UserHomeDir:       os.UserHomeDir,
		LookupEnv:         os.LookupEnv,
		DiscoverGitConfig: discoverHostGitConfig,
	}
}

func resolveHostEnvironment(inputs HostEnvironmentInputs) (HostEnvironment, error) {
	if inputs.UserHomeDir == nil || inputs.LookupEnv == nil || inputs.DiscoverGitConfig == nil {
		return HostEnvironment{}, errors.New("host environment inputs are incomplete")
	}
	environment, err := resolveHostIdentity(inputs)
	if err != nil {
		return HostEnvironment{}, err
	}
	codexHome, err := resolveOptionalCodexHome(environment.HomeDir, inputs.LookupEnv)
	if err != nil {
		return HostEnvironment{}, err
	}
	if codexHome != "" {
		environment.CodexHome = codexHome
	}
	claudeConfigDir, claudeConfigFile, err := resolveOptionalClaudeState(environment.HomeDir, inputs.LookupEnv)
	if err != nil {
		return HostEnvironment{}, err
	}
	environment.ClaudeConfigDir = claudeConfigDir
	environment.ClaudeConfigFile = claudeConfigFile
	personalSkills, err := resolveOptionalPersonalSkills(environment.HomeDir)
	if err != nil {
		return HostEnvironment{}, err
	}
	if personalSkills != "" {
		environment.PersonalSkills = personalSkills
	}
	return environment, nil
}

// resolveOptionalClaudeState returns either one explicit CLAUDE_CONFIG_DIR or the complete pair of
// default ~/.claude and ~/.claude.json paths. A partial default is treated as absent so claude-safe
// does not persist only half of Claude's state under a different layout inside the container.
func resolveOptionalClaudeState(home string, lookupEnv func(string) (string, bool)) (string, string, error) {
	if requested, found := lookupEnv("CLAUDE_CONFIG_DIR"); found && strings.TrimSpace(requested) != "" {
		source := strings.TrimSpace(requested)
		path, present, err := canonicalOptionalDirectory("Claude config directory", source)
		if err != nil {
			return "", "", err
		}
		if !present {
			return "", "", fmt.Errorf("Claude config directory %q does not exist", source)
		}
		return path, "", nil
	}

	directory, directoryPresent, err := canonicalOptionalDirectory(
		"Claude config directory", filepath.Join(home, ".claude"),
	)
	if err != nil {
		return "", "", err
	}
	config, configPresent, err := canonicalOptionalRegularFile(
		"Claude global config", filepath.Join(home, ".claude.json"),
	)
	if err != nil {
		return "", "", err
	}
	if !directoryPresent || !configPresent {
		return "", "", nil
	}
	return directory, config, nil
}

func resolveOptionalCodexHome(home string, lookupEnv func(string) (string, bool)) (string, error) {
	source := filepath.Join(home, ".codex")
	explicit := false
	if requested, found := lookupEnv("CODEX_HOME"); found && strings.TrimSpace(requested) != "" {
		source = strings.TrimSpace(requested)
		explicit = true
	}
	path, present, err := canonicalOptionalDirectory("Codex home", source)
	if err != nil {
		return "", err
	}
	if !present && explicit {
		return "", fmt.Errorf("Codex home %q does not exist", source)
	}
	return path, nil
}

func resolveOptionalPersonalSkills(home string) (string, error) {
	path := filepath.Join(home, ".agents", "skills")
	resolved, present, err := canonicalOptionalDirectory("personal skills", path)
	if err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	return resolved, nil
}

func canonicalOptionalDirectory(label, path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("%s %q is a broken symlink", label, path)
		} else if err != nil {
			return "", false, fmt.Errorf("inspect %s %q: %w", label, path, err)
		}
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, fmt.Errorf("canonicalize %s %q: %w", label, path, err)
	}
	canonical = filepath.Clean(canonical)
	if canonical == string(filepath.Separator) {
		return "", false, fmt.Errorf("%s cannot be the filesystem root", label)
	}
	info, err = os.Stat(canonical)
	if err != nil {
		return "", false, fmt.Errorf("inspect %s %q: %w", label, canonical, err)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("%s %q is not a directory", label, canonical)
	}
	if err := launchplan.ValidateMountPath(label, canonical); err != nil {
		return "", false, err
	}
	return canonical, true, nil
}

func canonicalOptionalRegularFile(label, path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("%s %q is a broken symlink", label, path)
		} else if err != nil {
			return "", false, fmt.Errorf("inspect %s %q: %w", label, path, err)
		}
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, fmt.Errorf("canonicalize %s %q: %w", label, path, err)
	}
	canonical = filepath.Clean(canonical)
	info, err = os.Stat(canonical)
	if err != nil {
		return "", false, fmt.Errorf("inspect %s %q: %w", label, canonical, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s %q is not a regular file", label, canonical)
	}
	if err := launchplan.ValidateMountPath(label, canonical); err != nil {
		return "", false, err
	}
	return canonical, true, nil
}

func resolveHostIdentity(inputs HostEnvironmentInputs) (HostEnvironment, error) {
	if inputs.UserHomeDir == nil || inputs.DiscoverGitConfig == nil {
		return HostEnvironment{}, errors.New("host identity inputs are incomplete")
	}
	home, err := inputs.UserHomeDir()
	if err != nil {
		return HostEnvironment{}, fmt.Errorf("resolve host home directory: %w", err)
	}
	if !filepath.IsAbs(home) {
		return HostEnvironment{}, fmt.Errorf("host home directory %q is not absolute", home)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return HostEnvironment{}, fmt.Errorf("canonicalize host home directory %q: %w", home, err)
	}
	home = filepath.Clean(home)
	if home == string(filepath.Separator) {
		return HostEnvironment{}, errors.New("host home directory cannot be the filesystem root")
	}
	info, err := os.Stat(home)
	if err != nil {
		return HostEnvironment{}, fmt.Errorf("inspect host home directory %q: %w", home, err)
	}
	if !info.IsDir() {
		return HostEnvironment{}, fmt.Errorf("host home directory %q is not a directory", home)
	}
	if err := launchplan.ValidateMountPath("host home directory", home); err != nil {
		return HostEnvironment{}, err
	}
	gitConfig, err := inputs.DiscoverGitConfig(home)
	if err != nil {
		return HostEnvironment{}, err
	}
	return HostEnvironment{HomeDir: home, GitConfig: gitConfig}, nil
}

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
	if err := validateHostHome(docker.HostHome); err != nil {
		return err
	}
	return nil
}

func validateHostHome(hostHome string) error {
	if hostHome == "/" {
		return errors.New("host home directory cannot be the filesystem root")
	}
	return launchplan.ValidateMountPath("host home directory", hostHome)
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
