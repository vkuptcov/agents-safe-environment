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
	HomeDir        string
	GitConfig      string
	CodexHome      string
	PersonalSkills string
}

// ResolvedProjectConfig is the complete configuration and physical mount contract for one invocation.
type ResolvedProjectConfig struct {
	Config              projectenv.ProjectConfig
	Defaults            projectenv.ProjectConfig
	Resolution          launchplan.Resolution
	Options             launchplan.Options
	DefaultCodexHomeSet bool
}

// ResolveProjectConfig applies the documented defaults -> TOML -> explicit-flags order for one discovered project.
func ResolveProjectConfig(
	project gitproject.Project,
	host HostEnvironment,
	defaultImage string,
	overrides launchplan.Overrides,
) (ResolvedProjectConfig, error) {
	defaults, err := DefaultProjectConfig(project, host, defaultImage)
	if err != nil {
		return ResolvedProjectConfig{}, err
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
	if err := projectenv.Validate(config); err != nil {
		return ResolvedProjectConfig{}, err
	}
	resolution, err := launchplan.Resolve(project, defaults, config)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	_, defaultCodexHomeSet := findRole(defaults.Common.Mounts, projectenv.RoleCodexHome)
	return ResolvedProjectConfig{
		Config:     config,
		Defaults:   defaults,
		Resolution: resolution,
		Options: launchplan.Options{
			ImageOverride: overrides.ImageOverride,
			NoHostMCP:     config.Common.NoHostMCP,
		},
		DefaultCodexHomeSet: defaultCodexHomeSet,
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

	mounts := make([]projectenv.MountConfig, 0, 7)
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
		Common: projectenv.CommonConfig{Image: image, Mounts: mounts},
		Codex:  projectenv.CodexConfig{Arguments: append([]string(nil), codexDefaultSandboxArgs...)},
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
