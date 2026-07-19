package launcher

import (
	"fmt"
	"path/filepath"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// HostEnvironment is the Docker-free host state needed to serialize project defaults and build launch requests.
type HostEnvironment struct {
	HomeDir        string
	GitConfig      string
	CodexHome      string
	PersonalSkills string
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
	if err := projectenv.Validate(projectenv.ProjectConfig{
		Common: projectenv.CommonConfig{Image: "validation-image"},
	}); err != nil {
		return err
	}
	if host.HomeDir == "" {
		return fmt.Errorf("host home directory is empty")
	}
	if err := validateConfiguredHostPath("host home directory", host.HomeDir, false); err != nil {
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
		if err := validateConfiguredHostPath(value.label, value.path, true); err != nil {
			return err
		}
	}
	return nil
}

func validateConfiguredHostPath(label string, path string, source bool) error {
	mount := projectenv.MountConfig{Role: projectenv.RoleAdditional, Source: path, Target: path}
	if !source {
		mount.Source = "/host-environment-validation"
		mount.Target = path
	}
	if err := projectenv.Validate(projectenv.ProjectConfig{
		Common: projectenv.CommonConfig{Image: "validation-image", Mounts: []projectenv.MountConfig{mount}},
	}); err != nil {
		return fmt.Errorf("validate %s: %w", label, err)
	}
	return nil
}
