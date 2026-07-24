// Package container implements the privileged bootstrap and lifecycle that run inside the
// agents-safe Sysbox container.
package container

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

const (
	defaultDockerReadyTimeout    = 60 * time.Second
	defaultDockerShutdownTimeout = 15 * time.Second
	tmpfsMountsEnvironment       = "AGENTS_SAFE_TMPFS_MOUNTS"
)

var accountNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*[$]?$`)

// TmpfsMount is one container-local filesystem that privileged bootstrap must make effective
// after Sysbox has attached the broader project bind.
type TmpfsMount struct {
	Target string `json:"target"`
	Mode   string `json:"mode"`
	// Owned marks a Python virtual-environment mask. Bootstrap chowns it to the host user with a
	// user-appropriate mode, so it does not appear as a root-owned sticky directory, and mounts it
	// executable, so the dynamic loader can map its native extension modules.
	Owned bool `json:"owned,omitempty"`
}

// Config is the validated host identity and timing policy received by the Go
// container entrypoint.
type Config struct {
	// HostUID is the numeric owner used by every managed command and writable
	// project mount.
	HostUID int
	// HostGID is the numeric primary group used by every managed command.
	HostGID int
	// HostUser is the login name recreated for HostUID inside the container.
	HostUser string
	// HostGroup is the primary group name recreated for HostGID inside the
	// container.
	HostGroup string
	// HostHome is created inside the container at the same absolute path used on
	// the host. The host home directory itself is not mounted.
	HostHome string
	// DockerReadyTimeout bounds nested-daemon startup and readiness checks.
	DockerReadyTimeout time.Duration
	// DockerShutdownTimeout bounds graceful nested-daemon shutdown.
	DockerShutdownTimeout time.Duration
	// HostMCP is the forwarded endpoint set, fixed at container creation and empty for a session
	// that forwards nothing.
	HostMCP []mcpchannel.Endpoint
	// TmpfsMounts repeats Docker's creation-time tmpfs plan inside privileged bootstrap. Sysbox
	// may attach the broader idmapped worktree bind after Docker's tmpfs and cover it.
	TmpfsMounts []TmpfsMount
}

// ConfigFromEnvironment parses the launcher contract without modifying the
// container. lookup normally is os.LookupEnv and is injectable in tests.
func ConfigFromEnvironment(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("environment lookup is nil")
	}

	hostUID, err := requiredNonNegativeInteger(lookup, "CODEX_SAFE_HOST_UID")
	if err != nil {
		return Config{}, err
	}
	hostGID, err := requiredNonNegativeInteger(lookup, "CODEX_SAFE_HOST_GID")
	if err != nil {
		return Config{}, err
	}
	hostUser, err := requiredAccountName(lookup, "CODEX_SAFE_HOST_USER")
	if err != nil {
		return Config{}, err
	}
	hostGroup, err := requiredAccountName(lookup, "CODEX_SAFE_HOST_GROUP")
	if err != nil {
		return Config{}, err
	}
	hostHome, found := lookup("CODEX_SAFE_HOST_HOME")
	if !found || hostHome == "" {
		return Config{}, fmt.Errorf("CODEX_SAFE_HOST_HOME is required")
	}
	if err := validateAbsolutePath("CODEX_SAFE_HOST_HOME", hostHome); err != nil {
		return Config{}, err
	}
	if hostHome == "/" {
		return Config{}, fmt.Errorf("CODEX_SAFE_HOST_HOME cannot be the filesystem root")
	}

	readyTimeout, err := optionalPositiveSeconds(
		lookup,
		"CODEX_SAFE_DOCKER_READY_TIMEOUT",
		defaultDockerReadyTimeout,
	)
	if err != nil {
		return Config{}, err
	}

	hostMCP, err := hostMCPEndpointsFromEnvironment(lookup)
	if err != nil {
		return Config{}, err
	}
	tmpfsMounts, err := tmpfsMountsFromEnvironment(lookup)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HostUID:               hostUID,
		HostGID:               hostGID,
		HostUser:              hostUser,
		HostGroup:             hostGroup,
		HostHome:              hostHome,
		DockerReadyTimeout:    readyTimeout,
		DockerShutdownTimeout: defaultDockerShutdownTimeout,
		HostMCP:               hostMCP,
		TmpfsMounts:           tmpfsMounts,
	}, nil
}

func tmpfsMountsFromEnvironment(lookup func(string) (string, bool)) ([]TmpfsMount, error) {
	value, found := lookup(tmpfsMountsEnvironment)
	if !found {
		return nil, nil
	}
	var mounts []TmpfsMount
	if err := json.Unmarshal([]byte(value), &mounts); err != nil || mounts == nil {
		return nil, fmt.Errorf("%s must be a JSON array of tmpfs mounts", tmpfsMountsEnvironment)
	}
	if err := validateTmpfsMounts(tmpfsMountsEnvironment, mounts); err != nil {
		return nil, err
	}
	return mounts, nil
}

func validateTmpfsMounts(label string, mounts []TmpfsMount) error {
	seen := make(map[string]struct{}, len(mounts))
	for index, mount := range mounts {
		name := fmt.Sprintf("%s[%d].target", label, index)
		if err := validateAbsolutePath(name, mount.Target); err != nil {
			return err
		}
		if mount.Target == "/" {
			return fmt.Errorf("%s cannot be the filesystem root", name)
		}
		if strings.Contains(mount.Target, ":") {
			return fmt.Errorf("%s cannot be represented safely with Docker --tmpfs", name)
		}
		if _, found := seen[mount.Target]; found {
			return fmt.Errorf("%s repeats target %q", label, mount.Target)
		}
		seen[mount.Target] = struct{}{}
		if len(mount.Mode) < 3 || len(mount.Mode) > 4 {
			return fmt.Errorf("%s[%d].mode must be a 3- or 4-digit octal mode", label, index)
		}
		if _, err := strconv.ParseUint(mount.Mode, 8, 16); err != nil {
			return fmt.Errorf("%s[%d].mode must be a 3- or 4-digit octal mode", label, index)
		}
	}
	return nil
}

func requiredNonNegativeInteger(lookup func(string) (string, bool), name string) (int, error) {
	value, found := lookup(name)
	if !found || value == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return parsed, nil
}

func requiredAccountName(lookup func(string) (string, bool), name string) (string, error) {
	value, found := lookup(name)
	if !found || !accountNamePattern.MatchString(value) {
		return "", fmt.Errorf("%s is not a supported account name", name)
	}
	return value, nil
}

func optionalPositiveSeconds(
	lookup func(string) (string, bool),
	name string,
	defaultValue time.Duration,
) (time.Duration, error) {
	value, found := lookup(name)
	if !found || value == "" {
		return defaultValue, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return time.Duration(seconds) * time.Second, nil
}

func validateAbsolutePath(name string, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s %q is not absolute", name, path)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("%s %q is not canonical", name, path)
	}
	if strings.ContainsAny(path, ",\x00\n\r") {
		return fmt.Errorf("%s %q contains unsupported characters", name, path)
	}
	return nil
}
