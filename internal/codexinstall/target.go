// Package codexinstall defines the shared, pure contract for the persistent container Codex
// installation store: protocol version, supported Linux targets, deterministic names, ownership
// labels, store paths, and release manifests.
//
// This package performs no host I/O. It has no os/exec, no Docker client, and mutates no filesystem
// state; it only validates and formats values that internal/launcher/dockercli, internal/launcher, and
// internal/container pass through it. Keeping it pure lets both the host launcher and the Linux
// container entrypoint depend on one identical definition of store identity without duplicating it.
package codexinstall

import "fmt"

// LinuxOS is the only container operating system this store protocol supports. The store identity
// always derives its target from the Docker daemon, never from the launcher host's own operating
// system, so this constant names the one value ParseTarget accepts for daemonOS.
const LinuxOS = "linux"

// Supported Linux container architectures. The architecture belongs to the container image and
// Docker execution target, not to any host Codex package.
const (
	ArchitectureAMD64 = "amd64"
	ArchitectureARM64 = "arm64"
)

// SupportedArchitectures lists every architecture ParseTarget accepts, in a stable order for
// diagnostics and tests.
var SupportedArchitectures = []string{ArchitectureAMD64, ArchitectureARM64}

// Target is the Linux container operating system and architecture an installation store, release, and
// session mount are built for.
type Target struct {
	Architecture string
}

// ParseTarget validates a Docker daemon's reported operating system and architecture and returns the
// Linux container target they select. It never falls back to a host runtime value: an unsupported or
// non-Linux daemon is a validation error, not a silent default, so a caller must derive the target
// from an inspected daemon rather than from runtime.GOOS/GOARCH.
func ParseTarget(daemonOS string, daemonArchitecture string) (Target, error) {
	if daemonOS != LinuxOS {
		return Target{}, fmt.Errorf(
			"unsupported Docker daemon operating system %q: only %q is supported", daemonOS, LinuxOS,
		)
	}
	target := Target{Architecture: daemonArchitecture}
	if err := target.Validate(); err != nil {
		return Target{}, fmt.Errorf("unsupported Docker daemon target: %w", err)
	}
	return target, nil
}

// Validate reports whether the target is a supported Linux container target. A Target returned by
// ParseTarget is always valid; Validate re-checks a Target assembled directly through the exported
// Architecture field, so a caller such as NewIdentity cannot form a store identity for an
// unsupported architecture like "386".
func (target Target) Validate() error {
	for _, supported := range SupportedArchitectures {
		if target.Architecture == supported {
			return nil
		}
	}
	return fmt.Errorf(
		"unsupported target architecture %q: supported architectures are %v",
		target.Architecture, SupportedArchitectures,
	)
}

// String returns the target's store-identity component, such as "linux-amd64".
func (target Target) String() string {
	return LinuxOS + "-" + target.Architecture
}
