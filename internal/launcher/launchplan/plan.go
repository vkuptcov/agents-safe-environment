// Package launchplan builds the validated filesystem contract passed from Git discovery to host
// container orchestration.
package launchplan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// Options are the per-launch choices the CLI resolves and the launcher applies. They live here, in
// the package both the CLI scaffold and the launcher already import, so neither has to depend on the
// other to name them.
type Options struct {
	// ImageOverride records explicit --image intent. Its value is independent of the selected image:
	// supplying the default reference still deliberately bypasses project-environment discovery.
	ImageOverride bool
	// NoHostMCP skips host MCP discovery entirely: no config.toml read, no forwarders, no mount, and
	// no relay. It selects creation-time state and so cannot narrow a session that already forwards.
	NoHostMCP bool
}

// Overrides records only launcher flags explicitly present in argv. The resolver applies these after loading the
// project file, preserving a configured false value when --no-host-mcp is omitted.
type Overrides struct {
	Image             string
	ImageOverride     bool
	NoHostMCP         bool
	NoHostMCPOverride bool
}

// BindMount describes one host path exposed to the Sysbox container through a Docker bind mount.
type BindMount struct {
	// Source is the canonical absolute path on the host.
	Source string
	// Target is the absolute path inside the container. It normally equals Source so Git and
	// nested Docker continue to see the same project paths as the host.
	Target string
	// ReadOnly prevents writes through this mount when true.
	ReadOnly bool
}

// Plan is the validated filesystem contract passed from Git-project discovery to the launcher.
// It identifies the managed worktree, preserves the caller's working directory, and restricts the host
// paths bind-mounted into the container.
type Plan struct {
	// ProjectRoot is the canonical root of the selected Git worktree. The launcher uses it as the
	// stable identity when it creates or reuses that worktree's managed container.
	ProjectRoot string
	// WorkingDir is the canonical caller directory within ProjectRoot. The launcher passes it to
	// Docker as the container's working directory.
	WorkingDir string
	// Mounts is the ordered, normalized set of host bind mounts for the container. Ordering
	// preserves the linked-worktree policy: a narrow writable Git mount follows its read-only parent.
	Mounts []BindMount
	// Provenance records the logical roles each normalized physical mount satisfies.
	Provenance []MountProvenance
	// HostMCPChannel reports whether the validated logical host_mcp_channel role is present.
	HostMCPChannel bool
}

// MountProvenance traces one physical bind to the logical roles that required it.
type MountProvenance struct {
	Mount BindMount
	Roles []projectenv.MountRole
}

// Degradation reports an omitted optional role. The public command decides whether its warning applies.
type Degradation struct {
	Role projectenv.MountRole
}

// Resolution is the role-validated physical launch plan and its optional-role degradations.
type Resolution struct {
	Plan         Plan
	Degradations []Degradation
}

// degradableRoleOrder is the canonical warning order for the optional roles the resolver may report
// as omitted. It is the single source of truth for both the Resolve degradation list and the CLI's
// output ordering, so the two cannot drift.
var degradableRoleOrder = []projectenv.MountRole{
	projectenv.RoleHostGitConfig,
	projectenv.RoleCodexHome,
	projectenv.RolePersonalSkills,
	projectenv.RoleHostMCPChannel,
}

// DegradationMessage returns the operator-facing warning text for an omitted optional role. It lives
// here, beside the role constants, so the generic CLI never spells out role names or warning text.
func DegradationMessage(role projectenv.MountRole) string {
	switch role {
	case projectenv.RoleHostGitConfig:
		return "mount role \"host_git_config\" is omitted; host Git identity and includes are unavailable"
	case projectenv.RoleCodexHome:
		return "mount role \"codex_home\" is omitted; host Codex state is unavailable; using ephemeral state"
	case projectenv.RolePersonalSkills:
		return "mount role \"personal_skills\" is omitted; personal skills are unavailable"
	case projectenv.RoleHostMCPChannel:
		return "mount role \"host_mcp_channel\" is omitted; host MCP forwarding is disabled"
	default:
		return fmt.Sprintf("mount role %q is omitted", role)
	}
}

// AppendCodexHomeAbsentDegradation inserts a synthetic codex_home degradation into an already-ordered
// degradation list at its canonical position. codex-safe reports it when the host never had a Codex
// home, a condition Resolve does not treat as a degradation because no default codex_home was omitted.
func AppendCodexHomeAbsentDegradation(degradations []Degradation) []Degradation {
	rank := degradableRank(projectenv.RoleCodexHome)
	result := make([]Degradation, 0, len(degradations)+1)
	inserted := false
	for _, degradation := range degradations {
		if !inserted && degradableRank(degradation.Role) > rank {
			result = append(result, Degradation{Role: projectenv.RoleCodexHome})
			inserted = true
		}
		result = append(result, degradation)
	}
	if !inserted {
		result = append(result, Degradation{Role: projectenv.RoleCodexHome})
	}
	return result
}

func degradableRank(role projectenv.MountRole) int {
	for index, candidate := range degradableRoleOrder {
		if candidate == role {
			return index
		}
	}
	return len(degradableRoleOrder)
}

// MountForRole returns the physical bind that satisfies one retained filesystem role.
func (plan Plan) MountForRole(role projectenv.MountRole) (BindMount, bool) {
	for _, provenance := range plan.Provenance {
		for _, candidate := range provenance.Roles {
			if candidate == role {
				return provenance.Mount, true
			}
		}
	}
	return BindMount{}, false
}

type rolePolicy struct {
	required bool
}

var managedRolePolicies = map[projectenv.MountRole]rolePolicy{
	projectenv.RoleHostGitConfig:   {},
	projectenv.RolePrimaryCheckout: {required: true},
	projectenv.RoleCommonGitDir:    {required: true},
	projectenv.RoleWorktree:        {required: true},
	projectenv.RoleCodexHome:       {},
	projectenv.RolePersonalSkills:  {},
	projectenv.RoleHostMCPChannel:  {},
}

// Resolve validates the typed logical mount snapshot and converts it into an ordered physical plan. Defaults define
// the host-specific identity of every managed role; comments are documentation and do not affect comparison.
func Resolve(
	project gitproject.Project,
	defaults projectenv.ProjectConfig,
	config projectenv.ProjectConfig,
) (Resolution, error) {
	if err := validateWorkingDirectory(project.RequestedDir, project.WorktreeRoot); err != nil {
		return Resolution{}, err
	}
	if err := projectenv.Validate(defaults); err != nil {
		return Resolution{}, fmt.Errorf("validate project defaults: %w", err)
	}
	if err := projectenv.Validate(config); err != nil {
		return Resolution{}, fmt.Errorf("validate project config: %w", err)
	}

	defaultRoles, err := managedRoleMap(defaults.Common.Mounts, "default")
	if err != nil {
		return Resolution{}, err
	}
	configRoles, err := managedRoleMap(config.Common.Mounts, "config")
	if err != nil {
		return Resolution{}, err
	}
	for role, policy := range managedRolePolicies {
		defaultMount, defaultPresent := defaultRoles[role]
		mount, present := configRoles[role]
		if policy.required && !defaultPresent {
			return Resolution{}, fmt.Errorf("default configuration omits required mount role %q", role)
		}
		if policy.required && !present {
			return Resolution{}, fmt.Errorf("required mount role %q is omitted", role)
		}
		if !present {
			continue
		}
		if !defaultPresent {
			return Resolution{}, fmt.Errorf("mount role %q is unavailable in this host default", role)
		}
		if !sameManagedMount(defaultMount, mount) {
			return Resolution{}, fmt.Errorf("mount role %q differs from the host-specific default", role)
		}
	}
	if err := validateProjectRoles(project, configRoles); err != nil {
		return Resolution{}, err
	}

	logical := make([]logicalMount, 0, len(config.Common.Mounts))
	for _, mount := range config.Common.Mounts {
		if mount.Role == projectenv.RoleHostMCPChannel {
			continue
		}
		if err := validateExistingMount(mount); err != nil {
			return Resolution{}, err
		}
		logical = append(logical, logicalMount{
			mount: BindMount{Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly},
			role:  mount.Role,
		})
	}
	physical, provenance, err := normalizeLogicalMounts(logical)
	if err != nil {
		return Resolution{}, err
	}

	_, hostMCPChannel := configRoles[projectenv.RoleHostMCPChannel]
	degradations := make([]Degradation, 0, len(degradableRoleOrder))
	for _, role := range degradableRoleOrder {
		if _, defaultPresent := defaultRoles[role]; !defaultPresent {
			continue
		}
		if _, present := configRoles[role]; !present {
			degradations = append(degradations, Degradation{Role: role})
		}
	}
	return Resolution{
		Plan: Plan{
			ProjectRoot:    project.WorktreeRoot,
			WorkingDir:     project.RequestedDir,
			Mounts:         physical,
			Provenance:     provenance,
			HostMCPChannel: hostMCPChannel,
		},
		Degradations: degradations,
	}, nil
}

func managedRoleMap(
	mounts []projectenv.MountConfig,
	source string,
) (map[projectenv.MountRole]projectenv.MountConfig, error) {
	roles := make(map[projectenv.MountRole]projectenv.MountConfig, len(managedRolePolicies))
	for _, mount := range mounts {
		if _, managed := managedRolePolicies[mount.Role]; !managed {
			continue
		}
		if _, exists := roles[mount.Role]; exists {
			return nil, fmt.Errorf("%s configuration repeats mount role %q", source, mount.Role)
		}
		roles[mount.Role] = mount
	}
	return roles, nil
}

func sameManagedMount(first, second projectenv.MountConfig) bool {
	return first.Role == second.Role && first.Source == second.Source && first.Target == second.Target &&
		first.ReadOnly == second.ReadOnly
}

func validateProjectRoles(project gitproject.Project, roles map[projectenv.MountRole]projectenv.MountConfig) error {
	expected := map[projectenv.MountRole]BindMount{
		projectenv.RoleWorktree: {
			Source: project.WorktreeRoot,
			Target: project.WorktreeRoot,
		},
		projectenv.RolePrimaryCheckout: {
			Source:   project.PrimaryRoot,
			Target:   project.PrimaryRoot,
			ReadOnly: project.Linked,
		},
		projectenv.RoleCommonGitDir: {
			Source: project.CommonGitDir,
			Target: project.CommonGitDir,
		},
	}
	for role, want := range expected {
		mount, found := roles[role]
		if !found {
			return fmt.Errorf("required mount role %q is omitted", role)
		}
		if mount.Source != want.Source || mount.Target != want.Target || mount.ReadOnly != want.ReadOnly {
			return fmt.Errorf("required mount role %q does not match the discovered Git topology", role)
		}
	}
	return nil
}

type logicalMount struct {
	mount BindMount
	role  projectenv.MountRole
}

func validateExistingMount(mount projectenv.MountConfig) error {
	if err := ValidateMountPath("mount source", mount.Source); err != nil {
		return err
	}
	if mount.Source == string(filepath.Separator) {
		return fmt.Errorf("mount source %q cannot be the filesystem root", mount.Source)
	}
	if err := ValidateMountPath("mount target", mount.Target); err != nil {
		return err
	}
	info, err := os.Stat(mount.Source)
	if err != nil {
		return fmt.Errorf("inspect mount source %q: %w", mount.Source, err)
	}
	if mount.Role == projectenv.RoleHostGitConfig {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("host Git config %q is not a regular file", mount.Source)
		}
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("mount source %q is not a directory", mount.Source)
	}
	return nil
}

func normalizeLogicalMounts(logical []logicalMount) ([]BindMount, []MountProvenance, error) {
	byMount := make(map[BindMount]int, len(logical))
	merged := make([]MountProvenance, 0, len(logical))
	for _, item := range logical {
		if index, found := byMount[item.mount]; found {
			merged[index].Roles = append(merged[index].Roles, item.role)
			continue
		}
		for _, existing := range merged {
			if existing.Mount.Target == item.mount.Target {
				return nil, nil, fmt.Errorf("conflicting mounts for target %q", item.mount.Target)
			}
		}
		byMount[item.mount] = len(merged)
		merged = append(merged, MountProvenance{Mount: item.mount, Roles: []projectenv.MountRole{item.role}})
	}

	merged = orderMountParentsFirst(merged)
	retained := make([]MountProvenance, 0, len(merged))
	for _, candidate := range merged {
		redundant := false
		for index := range retained {
			existing := &retained[index]
			if mountContains(existing.Mount, candidate.Mount) {
				if existing.Mount.ReadOnly == candidate.Mount.ReadOnly {
					existing.Roles = append(existing.Roles, candidate.Roles...)
					redundant = true
					break
				}
				continue
			}
			if PathsOverlap(existing.Mount.Source, candidate.Mount.Source) ||
				PathsOverlap(existing.Mount.Target, candidate.Mount.Target) {
				return nil, nil, fmt.Errorf("unsafe overlapping mounts %+v and %+v", existing.Mount, candidate.Mount)
			}
		}
		if !redundant {
			retained = append(retained, candidate)
		}
	}
	mounts := make([]BindMount, 0, len(retained))
	for _, mount := range retained {
		mounts = append(mounts, mount.Mount)
	}
	return mounts, retained, nil
}

// orderMountParentsFirst is a stable topological ordering of the mount-containment relation.
// It chooses the first available input entry while ensuring every parent precedes each nested child.
func orderMountParentsFirst(mounts []MountProvenance) []MountProvenance {
	ordered := make([]MountProvenance, 0, len(mounts))
	placed := make([]bool, len(mounts))
	for len(ordered) < len(mounts) {
		for candidate := range mounts {
			if placed[candidate] {
				continue
			}
			hasUnplacedParent := false
			for parent := range mounts {
				if parent == candidate || placed[parent] {
					continue
				}
				if mountContains(mounts[parent].Mount, mounts[candidate].Mount) {
					hasUnplacedParent = true
					break
				}
			}
			if hasUnplacedParent {
				continue
			}
			ordered = append(ordered, mounts[candidate])
			placed[candidate] = true
		}
	}
	return ordered
}

func mountContains(parent, child BindMount) bool {
	sourceRelative, err := filepath.Rel(parent.Source, child.Source)
	if err != nil || sourceRelative == ".." || strings.HasPrefix(sourceRelative, ".."+string(filepath.Separator)) {
		return false
	}
	targetRelative, err := filepath.Rel(parent.Target, child.Target)
	if err != nil || targetRelative == ".." || strings.HasPrefix(targetRelative, ".."+string(filepath.Separator)) {
		return false
	}
	return sourceRelative == targetRelative
}

// PathsOverlap reports whether either canonical absolute path contains the other or they are equal.
func PathsOverlap(first string, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent string, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateWorkingDirectory(workingDir string, worktreeRoot string) error {
	if err := ValidateMountPath("working directory", workingDir); err != nil {
		return err
	}
	if err := ValidateMountPath("working-tree root", worktreeRoot); err != nil {
		return err
	}

	relative, err := filepath.Rel(worktreeRoot, workingDir)
	if err != nil {
		return fmt.Errorf("compare working directory to working-tree root: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("working directory %q is outside working-tree root %q", workingDir, worktreeRoot)
	}
	return nil
}

// ValidateMountPath verifies one canonical absolute host or container path used in a bind-mount contract.
func ValidateMountPath(label string, path string) error {
	if path == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s %q is not absolute", label, path)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("%s %q is not canonical", label, path)
	}
	if strings.ContainsAny(path, ",\x00\n\r") {
		return fmt.Errorf("%s %q cannot be represented safely with Docker --mount", label, path)
	}
	return nil
}
