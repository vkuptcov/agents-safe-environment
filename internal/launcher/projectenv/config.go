package projectenv

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// MountRole identifies the purpose of a logical mount in the project configuration.
type MountRole string

const (
	RoleHostGitConfig   MountRole = "host_git_config"
	RolePrimaryCheckout MountRole = "primary_checkout"
	RoleCommonGitDir    MountRole = "common_git_dir"
	RoleWorktree        MountRole = "worktree"
	RoleCodexHome       MountRole = "codex_home"
	RolePersonalSkills  MountRole = "personal_skills"
	RoleHostMCPChannel  MountRole = "host_mcp_channel"
	RoleAdditional      MountRole = "additional"

	HostMCPChannelSource = "runtime://host-mcp-channel"
	HostMCPChannelTarget = "/run/codex-safe-host-mcp"
)

// ProjectConfig is the local, host-specific launcher configuration serialized by agents-safe init.
type ProjectConfig struct {
	Common CommonConfig `toml:"common"`
	Codex  CodexConfig  `toml:"codex"`
	Agents AgentsConfig `toml:"agents"`
}

// CommonConfig contains settings that affect both public launchers.
type CommonConfig struct {
	Image            string                  `toml:"image"`
	NoHostMCP        bool                    `toml:"no_host_mcp"`
	Mounts           []MountConfig           `toml:"mounts"`
	DependencyCaches []DependencyCacheConfig `toml:"dependency_caches"`
}

// DependencyCacheKind identifies a host dependency cache whose tool routing is owned by the launcher.
type DependencyCacheKind string

const (
	DependencyCacheGoBuild   DependencyCacheKind = "go_build"
	DependencyCacheGoModules DependencyCacheKind = "go_modules"
)

// DependencyCacheKindOrder is the canonical ordering the launcher applies to configured caches. It is the
// single source of truth for both init-time parsing and launch-time resolution, so the two cannot drift.
var DependencyCacheKindOrder = []DependencyCacheKind{DependencyCacheGoBuild, DependencyCacheGoModules}

// DependencyCacheConfig stores a host cache path. Source remains the tool-visible container target; launch
// resolution separately records the symlink-resolved physical bind source.
type DependencyCacheConfig struct {
	Kind   DependencyCacheKind `toml:"kind"`
	Source string              `toml:"source"`
}

// CodexConfig contains command-time defaults for codex-safe.
type CodexConfig struct {
	Arguments []string `toml:"arguments"`
}

// AgentsConfig reserves a typed section for future agents-safe defaults.
type AgentsConfig struct{}

// MountConfig describes a logical mount. The resolver validates role policy and produces physical binds.
type MountConfig struct {
	Role     MountRole `toml:"role"`
	Source   string    `toml:"source"`
	Target   string    `toml:"target"`
	ReadOnly bool      `toml:"read_only"`
	Comment  string    `toml:"comment"`
}

type configOverlay struct {
	Common *commonOverlay `toml:"common"`
	Codex  *codexOverlay  `toml:"codex"`
	Agents *agentsOverlay `toml:"agents"`
}

type commonOverlay struct {
	Image            *string                  `toml:"image"`
	NoHostMCP        *bool                    `toml:"no_host_mcp"`
	Mounts           *[]MountConfig           `toml:"mounts"`
	DependencyCaches *[]DependencyCacheConfig `toml:"dependency_caches"`
}

type codexOverlay struct {
	Arguments *[]string `toml:"arguments"`
}

type agentsOverlay struct{}

// Load overlays a regular local config file on a complete default configuration. Omitted fields retain the
// corresponding default while a present mounts array replaces the full list.
func Load(projectRoot string, defaults ProjectConfig) (ProjectConfig, error) {
	config := cloneConfig(defaults)
	if err := Validate(config); err != nil {
		return ProjectConfig{}, fmt.Errorf("validate project configuration defaults: %w", err)
	}

	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil || !exists {
		return config, err
	}
	path := filepath.Join(contextPath, ConfigName)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return ProjectConfig{}, fmt.Errorf("inspect project launcher config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return ProjectConfig{}, fmt.Errorf("project launcher config %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectConfig{}, fmt.Errorf("read project launcher config %q: %w", path, err)
	}

	var overlay configOverlay
	metadata, err := toml.Decode(string(data), &overlay)
	if err != nil {
		return ProjectConfig{}, fmt.Errorf("parse project launcher config %q: %w", path, err)
	}
	if unknown := metadata.Undecoded(); len(unknown) != 0 {
		return ProjectConfig{}, fmt.Errorf("project launcher config %q contains unknown setting %q", path, unknown[0])
	}
	applyOverlay(&config, overlay)
	if err := Validate(config); err != nil {
		return ProjectConfig{}, fmt.Errorf("validate project launcher config %q: %w", path, err)
	}
	return config, nil
}

// Encode writes the canonical typed TOML representation used by agents-safe init.
func Encode(config ProjectConfig, writer io.Writer) error {
	if err := Validate(config); err != nil {
		return err
	}
	if err := toml.NewEncoder(writer).Encode(config); err != nil {
		return fmt.Errorf("encode project launcher config: %w", err)
	}
	return nil
}

// Validate checks typed configuration without reading host paths. The mount resolver owns path existence, role identity,
// mode, and overlap validation after defaults and CLI overrides are known.
func Validate(config ProjectConfig) error {
	if config.Common.Image == "" || strings.TrimSpace(config.Common.Image) != config.Common.Image {
		return errors.New("common.image must be a non-empty trimmed image reference")
	}
	for index, arg := range config.Codex.Arguments {
		if arg == "" || strings.TrimSpace(arg) != arg || strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("codex.arguments[%d] is not a safe argv element", index)
		}
	}
	for index, mount := range config.Common.Mounts {
		if err := validateMount(index, mount); err != nil {
			return err
		}
	}
	seenCaches := make(map[DependencyCacheKind]struct{}, len(config.Common.DependencyCaches))
	for index, cache := range config.Common.DependencyCaches {
		if !supportedDependencyCacheKind(cache.Kind) {
			return fmt.Errorf("common.dependency_caches[%d].kind %q is unsupported", index, cache.Kind)
		}
		if _, found := seenCaches[cache.Kind]; found {
			return fmt.Errorf("common.dependency_caches repeats kind %q", cache.Kind)
		}
		seenCaches[cache.Kind] = struct{}{}
		if err := ValidatePath(fmt.Sprintf("common.dependency_caches[%d].source", index), cache.Source, true); err != nil {
			return err
		}
	}
	return nil
}

func applyOverlay(config *ProjectConfig, overlay configOverlay) {
	if overlay.Common != nil {
		if overlay.Common.Image != nil {
			config.Common.Image = *overlay.Common.Image
		}
		if overlay.Common.NoHostMCP != nil {
			config.Common.NoHostMCP = *overlay.Common.NoHostMCP
		}
		if overlay.Common.Mounts != nil {
			config.Common.Mounts = make([]MountConfig, len(*overlay.Common.Mounts))
			copy(config.Common.Mounts, *overlay.Common.Mounts)
		}
		if overlay.Common.DependencyCaches != nil {
			config.Common.DependencyCaches = append([]DependencyCacheConfig(nil), (*overlay.Common.DependencyCaches)...)
		}
	}
	if overlay.Codex != nil && overlay.Codex.Arguments != nil {
		config.Codex.Arguments = append([]string(nil), (*overlay.Codex.Arguments)...)
	}
}

func cloneConfig(config ProjectConfig) ProjectConfig {
	config.Common.Mounts = append([]MountConfig(nil), config.Common.Mounts...)
	config.Common.DependencyCaches = append([]DependencyCacheConfig(nil), config.Common.DependencyCaches...)
	config.Codex.Arguments = append([]string(nil), config.Codex.Arguments...)
	return config
}

func supportedDependencyCacheKind(kind DependencyCacheKind) bool {
	return kind == DependencyCacheGoBuild || kind == DependencyCacheGoModules
}

func validateMount(index int, mount MountConfig) error {
	if !supportedRole(mount.Role) {
		return fmt.Errorf("common.mounts[%d].role %q is unsupported", index, mount.Role)
	}
	if strings.ContainsAny(mount.Comment, "\x00\n\r") {
		return fmt.Errorf("common.mounts[%d].comment is not a single-line string", index)
	}
	if mount.Role == RoleHostMCPChannel {
		if mount.Source != HostMCPChannelSource || mount.Target != HostMCPChannelTarget || mount.ReadOnly {
			return fmt.Errorf("common.mounts[%d] has an invalid host MCP channel mount", index)
		}
		return nil
	}
	if err := ValidatePath(fmt.Sprintf("common.mounts[%d].source", index), mount.Source, true); err != nil {
		return err
	}
	return ValidatePath(fmt.Sprintf("common.mounts[%d].target", index), mount.Target, false)
}

func supportedRole(role MountRole) bool {
	switch role {
	case RoleHostGitConfig, RolePrimaryCheckout, RoleCommonGitDir, RoleWorktree, RoleCodexHome,
		RolePersonalSkills, RoleHostMCPChannel, RoleAdditional:
		return true
	default:
		return false
	}
}

// ValidatePath checks one configured bind-mount path without touching the filesystem. A source path is additionally
// rejected when it is the filesystem root; targets skip that check. Callers that validate a bare host path (rather
// than a full MountConfig) use it directly instead of assembling a throwaway ProjectConfig.
func ValidatePath(label string, path string, source bool) error {
	if path == "" || strings.TrimSpace(path) != path || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s must be a canonical absolute path", label)
	}
	if strings.ContainsAny(path, ",\x00\n\r") {
		return fmt.Errorf("%s cannot be represented safely with Docker --mount", label)
	}
	if source && path == string(filepath.Separator) {
		return fmt.Errorf("%s cannot be the filesystem root", label)
	}
	return nil
}
