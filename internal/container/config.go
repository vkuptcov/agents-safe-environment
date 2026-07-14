// Package container implements the privileged lifecycle of one outer
// codex-safe Sysbox container.
package container

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDockerReadyTimeout    = 60 * time.Second
	defaultDockerShutdownTimeout = 15 * time.Second
)

var accountNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*[$]?$`)

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

	return Config{
		HostUID:               hostUID,
		HostGID:               hostGID,
		HostUser:              hostUser,
		HostGroup:             hostGroup,
		HostHome:              hostHome,
		DockerReadyTimeout:    readyTimeout,
		DockerShutdownTimeout: defaultDockerShutdownTimeout,
	}, nil
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
