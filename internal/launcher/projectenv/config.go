package projectenv

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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
	RoleClaudeHome      MountRole = "claude_home"
	RoleClaudeConfig    MountRole = "claude_config"
	RolePersonalSkills  MountRole = "personal_skills"
	RoleHostMCPChannel  MountRole = "host_mcp_channel"
	RoleAdditional      MountRole = "additional"

	HostMCPChannelSource = "runtime://host-mcp-channel"
	HostMCPChannelTarget = "/run/agents-safe-host-mcp"
	DefaultTmpfsMode     = "1777"
)

// ProjectConfig is the resolved launcher configuration: defaults overlaid by common.toml and config.toml.
type ProjectConfig struct {
	Common CommonConfig `toml:"common"`
	Codex  CodexConfig  `toml:"codex"`
	Claude ClaudeConfig `toml:"claude"`
	Agents AgentsConfig `toml:"agents"`
}

// CommonConfig contains settings that affect every public launcher. Its two embedded halves are the single
// classification of which keys are portable (common.toml) and which name host paths (config.toml only); TOML
// flattens them into one [common] table.
type CommonConfig struct {
	PortableCommonConfig
	HostCommonConfig
}

// PortableCommonConfig holds the [common] scalars that may be shared through the tracked common.toml.
type PortableCommonConfig struct {
	DockerStorage     DockerStorageMode `toml:"docker_storage"`
	Image             string            `toml:"image"`
	NoHostMCP         bool              `toml:"no_host_mcp"`
	UseHostPythonVenv bool              `toml:"use_host_python_venv"`
	KeepContainer     bool              `toml:"keep_container"`
}

// HostCommonConfig holds the [common] lists that name host or worktree paths; only config.toml may set them.
type HostCommonConfig struct {
	Mounts           []MountConfig           `toml:"mounts"`
	TmpfsMounts      []TmpfsMountConfig      `toml:"tmpfs_mounts,omitempty"`
	DependencyCaches []DependencyCacheConfig `toml:"dependency_caches"`
}

// TmpfsMountConfig describes one container-local writable filesystem. It has no host source.
type TmpfsMountConfig struct {
	Target  string `toml:"target"`
	Mode    string `toml:"mode"`
	Comment string `toml:"comment"`
}

// DockerStorageMode selects which identity owns the nested Docker daemon's persistent named volume.
type DockerStorageMode string

const (
	DockerStorageBranch  DockerStorageMode = "branch"
	DockerStorageProject DockerStorageMode = "project"
	DockerStorageShared  DockerStorageMode = "shared"
)

// DockerStorageModeOrder is the single source of truth for the supported scopes. Validation, the default
// configuration, and launcher help text all derive from it, so the vocabulary cannot drift.
var DockerStorageModeOrder = []DockerStorageMode{
	DockerStorageBranch,
	DockerStorageProject,
	DockerStorageShared,
}

// DefaultDockerStorage is the isolating scope every launch selects unless configuration or a flag overrides it.
const DefaultDockerStorage = DockerStorageBranch

// DependencyCacheKind identifies a host dependency cache whose tool routing is owned by the launcher.
type DependencyCacheKind string

const (
	DependencyCacheGoBuild   DependencyCacheKind = "go_build"
	DependencyCacheGoModules DependencyCacheKind = "go_modules"
	DependencyCacheUV        DependencyCacheKind = "uv"
)

// DependencyCacheKindOrder is the canonical ordering the launcher applies to configured caches. It is the
// single source of truth for both init-time parsing and launch-time resolution, so the two cannot drift.
var DependencyCacheKindOrder = []DependencyCacheKind{
	DependencyCacheGoBuild,
	DependencyCacheGoModules,
	DependencyCacheUV,
}

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

// ClaudeConfig contains command-time defaults for claude-safe.
type ClaudeConfig struct {
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
	Claude *claudeOverlay `toml:"claude"`
	Agents *agentsOverlay `toml:"agents"`
}

type commonOverlay struct {
	DockerStorage     *DockerStorageMode       `toml:"docker_storage"`
	Image             *string                  `toml:"image"`
	NoHostMCP         *bool                    `toml:"no_host_mcp"`
	UseHostPythonVenv *bool                    `toml:"use_host_python_venv"`
	KeepContainer     *bool                    `toml:"keep_container"`
	Mounts            *[]MountConfig           `toml:"mounts"`
	TmpfsMounts       *[]TmpfsMountConfig      `toml:"tmpfs_mounts"`
	DependencyCaches  *[]DependencyCacheConfig `toml:"dependency_caches"`
}

type codexOverlay struct {
	Arguments *[]string `toml:"arguments"`
}

type claudeOverlay struct {
	Arguments *[]string `toml:"arguments"`
}

type agentsOverlay struct{}

// ConfigFileExists reports whether projectRoot carries a persisted local config.toml. It applies the same
// presence test as LoadLayers, so callers can decide whether to seed config-less defaults (for example
// auto-discovered dependency caches) that a written config would otherwise own. A tracked common.toml never
// counts: it cannot declare caches, so its presence must not suppress discovery.
func ConfigFileExists(projectRoot string) (bool, error) {
	_, exists, err := locateConfigFile(projectRoot, ConfigName)
	return exists, err
}

// locateConfigFile resolves one launcher config file under projectRoot and reports whether it is a usable
// regular file. A missing project-environment directory or config file is a non-error absence; only an
// unreadable or non-regular entry is an error.
func locateConfigFile(projectRoot string, name string) (string, bool, error) {
	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil || !exists {
		return "", false, err
	}
	path := filepath.Join(contextPath, name)
	exists, err = existingRegularFile(path, "project launcher config")
	return path, exists, err
}

// existingRegularFile reports whether path is a regular file. Absence is not an error; any other entry kind is.
func existingRegularFile(path string, label string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s %q is not a regular file", label, path)
	}
	return true, nil
}

// configLayer is one optional config file, applied in configLayers order over the base configuration.
type configLayer struct {
	name string
	// portable layers are shared across hosts and therefore must not set HostCommonConfig keys.
	portable bool
}

var configLayers = []configLayer{
	{name: CommonConfigName, portable: true},
	{name: ConfigName},
}

// LoadLayers overlays every config layer on a complete base configuration. Omitted fields retain the lower
// layer's value while a present list replaces the full list. It also returns one sorted warning per key whose
// value in a higher layer differs from the value a lower layer set.
func LoadLayers(projectRoot string, base ProjectConfig) (ProjectConfig, []string, error) {
	config := cloneConfig(base)
	if err := Validate(config); err != nil {
		return ProjectConfig{}, nil, fmt.Errorf("validate project configuration defaults: %w", err)
	}

	values := make([]map[string]any, len(configLayers))
	for index, layer := range configLayers {
		layerValues, err := decodeLayer(projectRoot, layer, &config)
		if err != nil {
			return ProjectConfig{}, nil, err
		}
		values[index] = layerValues
	}
	var warnings []string
	for upper := range configLayers {
		for lower := range upper {
			for _, key := range shadowedKeys(values[lower], values[upper]) {
				warnings = append(warnings, fmt.Sprintf("%s overrides %s for %q",
					configLayers[upper].name, configLayers[lower].name, key))
			}
		}
	}
	return config, warnings, nil
}

// decodeLayer overlays one optional config file on config, validates the result, and returns the file's
// values keyed by dotted TOML key. An absent file returns no values and leaves config unchanged.
func decodeLayer(projectRoot string, layer configLayer, config *ProjectConfig) (map[string]any, error) {
	path, exists, err := locateConfigFile(projectRoot, layer.name)
	if err != nil || !exists {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read project launcher config %q: %w", path, err)
	}

	var overlay configOverlay
	metadata, err := toml.Decode(string(data), &overlay)
	if err != nil {
		return nil, fmt.Errorf("parse project launcher config %q: %w", path, err)
	}
	if unknown := metadata.Undecoded(); len(unknown) != 0 {
		return nil, fmt.Errorf("project launcher config %q contains unknown setting %q", path, unknown[0])
	}
	if layer.portable {
		for _, key := range hostCommonKeys() {
			if metadata.IsDefined("common", key) {
				return nil, fmt.Errorf(
					"project common config %q must not set common.%s; host-specific paths belong in %s",
					path, key, ConfigName)
			}
		}
	}
	applyOverlay(config, overlay)
	if err := Validate(*config); err != nil {
		return nil, fmt.Errorf("validate project launcher config %q: %w", path, err)
	}
	var tables map[string]map[string]any
	if _, err := toml.Decode(string(data), &tables); err != nil {
		return nil, fmt.Errorf("parse project launcher config %q: %w", path, err)
	}
	values := make(map[string]any)
	for table, entries := range tables {
		for key, value := range entries {
			values[table+"."+key] = value
		}
	}
	return values, nil
}

// hostCommonKeys returns the TOML keys of HostCommonConfig, so the portable-layer rule follows the struct.
func hostCommonKeys() []string {
	fields := reflect.VisibleFields(reflect.TypeFor[HostCommonConfig]())
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		key, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
		keys = append(keys, key)
	}
	return keys
}

// shadowedKeys lists, sorted, the keys present in both layers whose decoded values differ.
func shadowedKeys(lower map[string]any, upper map[string]any) []string {
	var keys []string
	for key, value := range upper {
		if lowerValue, found := lower[key]; found && !reflect.DeepEqual(lowerValue, value) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// Encode writes the complete canonical typed TOML representation, which config.toml may always contain.
func Encode(config ProjectConfig, writer io.Writer) error {
	return encodeValidated(config, config, writer)
}

// EncodeCommon writes the portable settings of config in the tracked common.toml shape.
func EncodeCommon(config ProjectConfig, writer io.Writer) error {
	return encodeValidated(config, struct {
		Common PortableCommonConfig `toml:"common"`
		Codex  CodexConfig          `toml:"codex"`
		Claude ClaudeConfig         `toml:"claude"`
		Agents AgentsConfig         `toml:"agents"`
	}{config.Common.PortableCommonConfig, config.Codex, config.Claude, config.Agents}, writer)
}

// EncodeLocal writes the host-specific lists of config in the config.toml shape produced by init, so a new
// worktree never shadows the shared portable values.
func EncodeLocal(config ProjectConfig, writer io.Writer) error {
	return encodeValidated(config, struct {
		Common HostCommonConfig `toml:"common"`
	}{config.Common.HostCommonConfig}, writer)
}

func encodeValidated(config ProjectConfig, value any, writer io.Writer) error {
	if err := Validate(config); err != nil {
		return err
	}
	if err := toml.NewEncoder(writer).Encode(value); err != nil {
		return fmt.Errorf("encode project launcher config: %w", err)
	}
	return nil
}

// Validate checks typed configuration without reading host paths. The mount resolver owns path existence, role identity,
// mode, and overlap validation after defaults and CLI overrides are known.
func Validate(config ProjectConfig) error {
	if err := ValidateDockerStorage(config.Common.DockerStorage); err != nil {
		return err
	}
	if config.Common.Image == "" || strings.TrimSpace(config.Common.Image) != config.Common.Image {
		return errors.New("common.image must be a non-empty trimmed image reference")
	}
	for index, arg := range config.Codex.Arguments {
		if arg == "" || strings.TrimSpace(arg) != arg || strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("codex.arguments[%d] is not a safe argv element", index)
		}
	}
	for index, arg := range config.Claude.Arguments {
		if arg == "" || strings.TrimSpace(arg) != arg || strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("claude.arguments[%d] is not a safe argv element", index)
		}
	}
	for index, mount := range config.Common.Mounts {
		if err := validateMount(index, mount); err != nil {
			return err
		}
	}
	seenTmpfsTargets := make(map[string]struct{}, len(config.Common.TmpfsMounts))
	for index, mount := range config.Common.TmpfsMounts {
		if err := validateTmpfsMount(index, mount); err != nil {
			return err
		}
		if _, found := seenTmpfsTargets[mount.Target]; found {
			return fmt.Errorf("common.tmpfs_mounts repeats target %q", mount.Target)
		}
		seenTmpfsTargets[mount.Target] = struct{}{}
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
		if overlay.Common.DockerStorage != nil {
			config.Common.DockerStorage = *overlay.Common.DockerStorage
		}
		if overlay.Common.Image != nil {
			config.Common.Image = *overlay.Common.Image
		}
		if overlay.Common.NoHostMCP != nil {
			config.Common.NoHostMCP = *overlay.Common.NoHostMCP
		}
		if overlay.Common.UseHostPythonVenv != nil {
			config.Common.UseHostPythonVenv = *overlay.Common.UseHostPythonVenv
		}
		if overlay.Common.KeepContainer != nil {
			config.Common.KeepContainer = *overlay.Common.KeepContainer
		}
		if overlay.Common.Mounts != nil {
			config.Common.Mounts = make([]MountConfig, len(*overlay.Common.Mounts))
			copy(config.Common.Mounts, *overlay.Common.Mounts)
		}
		if overlay.Common.TmpfsMounts != nil {
			config.Common.TmpfsMounts = append([]TmpfsMountConfig(nil), (*overlay.Common.TmpfsMounts)...)
		}
		if overlay.Common.DependencyCaches != nil {
			config.Common.DependencyCaches = append([]DependencyCacheConfig(nil), (*overlay.Common.DependencyCaches)...)
		}
	}
	if overlay.Codex != nil && overlay.Codex.Arguments != nil {
		config.Codex.Arguments = append([]string(nil), (*overlay.Codex.Arguments)...)
	}
	if overlay.Claude != nil && overlay.Claude.Arguments != nil {
		config.Claude.Arguments = append([]string(nil), (*overlay.Claude.Arguments)...)
	}
}

func cloneConfig(config ProjectConfig) ProjectConfig {
	config.Common.Mounts = append([]MountConfig(nil), config.Common.Mounts...)
	config.Common.TmpfsMounts = append([]TmpfsMountConfig(nil), config.Common.TmpfsMounts...)
	config.Common.DependencyCaches = append([]DependencyCacheConfig(nil), config.Common.DependencyCaches...)
	config.Codex.Arguments = append([]string(nil), config.Codex.Arguments...)
	config.Claude.Arguments = append([]string(nil), config.Claude.Arguments...)
	return config
}

func validateTmpfsMount(index int, mount TmpfsMountConfig) error {
	if err := ValidatePath(fmt.Sprintf("common.tmpfs_mounts[%d].target", index), mount.Target, false); err != nil {
		return err
	}
	if strings.Contains(mount.Target, ":") {
		return fmt.Errorf("common.tmpfs_mounts[%d].target cannot be represented safely with Docker --tmpfs", index)
	}
	if len(mount.Mode) < 3 || len(mount.Mode) > 4 {
		return fmt.Errorf("common.tmpfs_mounts[%d].mode must be a 3- or 4-digit octal mode", index)
	}
	if _, err := strconv.ParseUint(mount.Mode, 8, 16); err != nil {
		return fmt.Errorf("common.tmpfs_mounts[%d].mode must be a 3- or 4-digit octal mode", index)
	}
	if strings.ContainsAny(mount.Comment, "\x00\n\r") {
		return fmt.Errorf("common.tmpfs_mounts[%d].comment is not a single-line string", index)
	}
	return nil
}

func supportedDependencyCacheKind(kind DependencyCacheKind) bool {
	return slices.Contains(DependencyCacheKindOrder, kind)
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
	case RoleHostGitConfig, RolePrimaryCheckout, RoleCommonGitDir, RoleWorktree, RoleCodexHome, RoleClaudeHome,
		RoleClaudeConfig,
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

// ValidateDockerStorage checks the supported nested-daemon storage scopes.
func ValidateDockerStorage(mode DockerStorageMode) error {
	if slices.Contains(DockerStorageModeOrder, mode) {
		return nil
	}
	return fmt.Errorf("common.docker_storage must be branch, project, or shared; got %q", mode)
}
