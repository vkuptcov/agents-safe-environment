package codexinstall

import "fmt"

// StoreProtocolVersion is the current wire/layout contract for the Codex installation store: volume
// naming, ownership labels, store paths, and release-manifest shape. Bumping it changes the
// deterministic volume name, so a protocol change never silently reuses an incompatible store.
const StoreProtocolVersion = 1

// Identity is the complete set of values that must match for two launches to share one Codex
// installation store: the invoking host UID, the store protocol, and the resolved Linux target.
type Identity struct {
	HostUID  int
	Protocol int
	Target   Target
}

// NewIdentity validates a host UID, store protocol, and target and returns the store identity they
// select. Most callers want NewCurrentIdentity; this constructor exists so protocol boundaries stay
// independently testable without waiting for a future protocol bump.
func NewIdentity(hostUID int, protocol int, target Target) (Identity, error) {
	if hostUID < 0 {
		return Identity{}, fmt.Errorf("invalid host UID %d", hostUID)
	}
	if protocol <= 0 {
		return Identity{}, fmt.Errorf("invalid store protocol %d", protocol)
	}
	if err := target.Validate(); err != nil {
		return Identity{}, err
	}
	return Identity{HostUID: hostUID, Protocol: protocol, Target: target}, nil
}

// NewCurrentIdentity returns the store identity for the current StoreProtocolVersion.
func NewCurrentIdentity(hostUID int, target Target) (Identity, error) {
	return NewIdentity(hostUID, StoreProtocolVersion, target)
}

// VolumeName returns the deterministic Docker volume name for this identity:
// codex-safe-codex-v<protocol>-<host-uid>-linux-<architecture>.
func (identity Identity) VolumeName() string {
	return fmt.Sprintf("codex-safe-codex-v%d-%d-%s", identity.Protocol, identity.HostUID, identity.Target.String())
}

// MaintenanceContainerName returns the deterministic name for the one trusted maintenance container
// allowed to publish into this identity's volume. A concurrent update sees this name already in use
// and reports that an update is already in progress rather than starting a second writer.
func (identity Identity) MaintenanceContainerName() string {
	return fmt.Sprintf(
		"codex-safe-codex-update-v%d-%d-%s", identity.Protocol, identity.HostUID, identity.Target.String(),
	)
}
