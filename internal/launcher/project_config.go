package launcher

import (
	"fmt"
	"path/filepath"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// HostEnvironment is the Docker-free host state needed to serialize project defaults and build launch requests.
type HostEnvironment struct {
	HomeDir          string
	GitConfig        string
	CodexHome        string
	ClaudeConfigDir  string
	ClaudeConfigFile string
	PersonalSkills   string
}

// ResolvedProjectConfig is the complete configuration and physical mount contract for one invocation.
type ResolvedProjectConfig struct {
	Config               projectenv.ProjectConfig
	Defaults             projectenv.ProjectConfig
	Resolution           launchplan.Resolution
	Options              launchplan.Options
	DefaultCodexHomeSet  bool
	DefaultClaudeHomeSet bool
}

// DefaultCacheDiscoverer resolves the dependency caches to seed into the defaults when a project carries
// no config.toml, so a config-less launch matches `agents-safe init` auto-discovery. It receives the
// cache-less defaults it is enriching and returns the caches to attach. A nil discoverer keeps the
// historical no-cache fallback.
type DefaultCacheDiscoverer func(
	project gitproject.Project,
	host HostEnvironment,
	defaults projectenv.ProjectConfig,
) ([]projectenv.DependencyCacheConfig, error)

// ResolveProjectConfig applies the documented defaults -> TOML -> explicit-flags order for one discovered project.
// When the project has no config.toml and discoverDefaultCaches is non-nil, the discovered caches seed the
// defaults so a config-less launch resolves the same contract `agents-safe init` would have written. A present
// config.toml stays authoritative, including when it deliberately declares no caches.
func ResolveProjectConfig(
	project gitproject.Project,
	host HostEnvironment,
	defaultImage string,
	overrides launchplan.Overrides,
	discoverDefaultCaches DefaultCacheDiscoverer,
) (ResolvedProjectConfig, error) {
	defaults, err := DefaultProjectConfig(project, host, defaultImage)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	if discoverDefaultCaches != nil {
		configExists, err := projectenv.ConfigFileExists(project.WorktreeRoot)
		if err != nil {
			return ResolvedProjectConfig{}, err
		}
		if !configExists {
			caches, err := discoverDefaultCaches(project, host, defaults)
			if err != nil {
				return ResolvedProjectConfig{}, err
			}
			defaults.Common.DependencyCaches = caches
		}
	}
	config, err := projectenv.Load(project.WorktreeRoot, defaults)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	if overrides.ImageOverride {
		config.Common.Image = overrides.Image
	}
	if overrides.NoHostMCPOverride {
		config.Common.NoHostMCP = overrides.NoHostMCP
	}
	if overrides.UseHostPythonVenvOverride {
		config.Common.UseHostPythonVenv = overrides.UseHostPythonVenv
	}
	resolution, err := launchplan.ResolveWithHostHome(project, defaults, config, host.HomeDir)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	_, defaultCodexHomeSet := findRole(defaults.Common.Mounts, projectenv.RoleCodexHome)
	_, defaultClaudeHomeSet := findRole(defaults.Common.Mounts, projectenv.RoleClaudeHome)
	return ResolvedProjectConfig{
		Config:     config,
		Defaults:   defaults,
		Resolution: resolution,
		Options: launchplan.Options{
			ImageOverride:     overrides.ImageOverride,
			NoHostMCP:         config.Common.NoHostMCP,
			UseHostPythonVenv: config.Common.UseHostPythonVenv,
		},
		DefaultCodexHomeSet:  defaultCodexHomeSet,
		DefaultClaudeHomeSet: defaultClaudeHomeSet,
	}, nil
}

func findRole(mounts []projectenv.MountConfig, role projectenv.MountRole) (projectenv.MountConfig, bool) {
	for _, mount := range mounts {
		if mount.Role == role {
			return mount, true
		}
	}
	return projectenv.MountConfig{}, false
}

// DefaultProjectConfig returns the full typed snapshot generated for one project and host environment.
func DefaultProjectConfig(
	project gitproject.Project,
	host HostEnvironment,
	image string,
) (projectenv.ProjectConfig, error) {
	if err := validateHostEnvironment(host); err != nil {
		return projectenv.ProjectConfig{}, err
	}
	if image == "" {
		return projectenv.ProjectConfig{}, fmt.Errorf("default image is empty")
	}

	mounts := make([]projectenv.MountConfig, 0, 9)
	if host.GitConfig != "" {
		mounts = append(mounts, projectenv.MountConfig{
			Role:     projectenv.RoleHostGitConfig,
			Source:   host.GitConfig,
			Target:   filepath.Join(host.HomeDir, ".gitconfig"),
			ReadOnly: true,
			Comment:  "Optional: expose host Git identity and includes read-only.",
		})
	}
	mounts = append(mounts,
		projectenv.MountConfig{
			Role:     projectenv.RolePrimaryCheckout,
			Source:   project.PrimaryRoot,
			Target:   project.PrimaryRoot,
			ReadOnly: project.Linked,
			Comment:  "Required: expose the primary checkout for Git topology.",
		},
		projectenv.MountConfig{
			Role:    projectenv.RoleCommonGitDir,
			Source:  project.CommonGitDir,
			Target:  project.CommonGitDir,
			Comment: "Required: keep shared Git metadata writable.",
		},
		projectenv.MountConfig{
			Role:    projectenv.RoleWorktree,
			Source:  project.WorktreeRoot,
			Target:  project.WorktreeRoot,
			Comment: "Required: expose the selected worktree writable.",
		},
	)
	if host.CodexHome != "" {
		mounts = append(mounts, projectenv.MountConfig{
			Role:    projectenv.RoleCodexHome,
			Source:  host.CodexHome,
			Target:  containerCodexHome(host.HomeDir),
			Comment: "Optional: persist host Codex state.",
		})
	}
	if host.ClaudeConfigDir != "" {
		mounts = append(mounts, projectenv.MountConfig{
			Role:    projectenv.RoleClaudeHome,
			Source:  host.ClaudeConfigDir,
			Target:  containerClaudeConfigDir(host.HomeDir),
			Comment: "Optional: persist host Claude Code state.",
		})
	}
	if host.ClaudeConfigFile != "" {
		mounts = append(mounts, projectenv.MountConfig{
			Role:    projectenv.RoleClaudeConfig,
			Source:  host.ClaudeConfigFile,
			Target:  filepath.Join(host.HomeDir, ".claude.json"),
			Comment: "Optional: persist host Claude Code global configuration.",
		})
	}
	if host.PersonalSkills != "" {
		mounts = append(mounts, projectenv.MountConfig{
			Role:     projectenv.RolePersonalSkills,
			Source:   host.PersonalSkills,
			Target:   filepath.Join(host.HomeDir, ".agents", "skills"),
			ReadOnly: true,
			Comment:  "Optional: expose personal skills read-only.",
		})
	}
	mounts = append(mounts, projectenv.MountConfig{
		Role:    projectenv.RoleHostMCPChannel,
		Source:  projectenv.HostMCPChannelSource,
		Target:  projectenv.HostMCPChannelTarget,
		Comment: "Optional: forward eligible host MCP endpoints.",
	})

	config := projectenv.ProjectConfig{
		Common: projectenv.CommonConfig{
			Image:  image,
			Mounts: mounts,
		},
		Codex:  projectenv.CodexConfig{Arguments: append([]string(nil), codexDefaultSandboxArgs...)},
		Claude: projectenv.ClaudeConfig{Arguments: append([]string(nil), claudeDefaultPermissionArgs...)},
	}
	if err := projectenv.Validate(config); err != nil {
		return projectenv.ProjectConfig{}, fmt.Errorf("validate generated project configuration: %w", err)
	}
	return config, nil
}

func validateHostEnvironment(host HostEnvironment) error {
	if host.HomeDir == "" {
		return fmt.Errorf("host home directory is empty")
	}
	// The home directory is validated as a mount target: it names a container path, not a bind source, so the
	// filesystem-root check that ValidatePath applies to sources does not apply here.
	if err := projectenv.ValidatePath("host home directory", host.HomeDir, false); err != nil {
		return err
	}
	for _, value := range []struct {
		label string
		path  string
	}{
		{"host Git config", host.GitConfig},
		{"host Codex home", host.CodexHome},
		{"host Claude config directory", host.ClaudeConfigDir},
		{"host Claude global config", host.ClaudeConfigFile},
		{"personal skills", host.PersonalSkills},
	} {
		if value.path == "" {
			continue
		}
		if err := projectenv.ValidatePath(value.label, value.path, true); err != nil {
			return err
		}
	}
	return nil
}
