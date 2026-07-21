package codexinstall

import "testing"

func TestNewIdentityRejectsInvalidBoundaries(t *testing.T) {
	t.Parallel()
	target := Target{Architecture: ArchitectureAMD64}
	cases := []struct {
		name     string
		hostUID  int
		protocol int
		target   Target
	}{
		{"negative uid", -1, StoreProtocolVersion, target},
		{"zero protocol", 1000, 0, target},
		{"negative protocol", 1000, -1, target},
		{"empty target", 1000, StoreProtocolVersion, Target{}},
		{"unsupported architecture", 1000, StoreProtocolVersion, Target{Architecture: "386"}},
	}
	for _, testCase := range cases {
		if _, err := NewIdentity(testCase.hostUID, testCase.protocol, testCase.target); err == nil {
			t.Errorf("%s: NewIdentity() must reject uid=%d protocol=%d target=%#v",
				testCase.name, testCase.hostUID, testCase.protocol, testCase.target)
		}
	}
}

func TestNewIdentityAcceptsBoundaryUID(t *testing.T) {
	t.Parallel()
	if _, err := NewIdentity(0, StoreProtocolVersion, Target{Architecture: ArchitectureAMD64}); err != nil {
		t.Fatalf("NewIdentity(uid=0) error = %v", err)
	}
}

func TestNewCurrentIdentityUsesStoreProtocolVersion(t *testing.T) {
	t.Parallel()
	identity, err := NewCurrentIdentity(1000, Target{Architecture: ArchitectureARM64})
	if err != nil {
		t.Fatalf("NewCurrentIdentity() error = %v", err)
	}
	if identity.Protocol != StoreProtocolVersion {
		t.Fatalf("identity.Protocol = %d, want %d", identity.Protocol, StoreProtocolVersion)
	}
}

func TestVolumeNameIsDeterministic(t *testing.T) {
	t.Parallel()
	identity := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}
	want := "codex-safe-codex-v1-1000-linux-amd64"
	if got := identity.VolumeName(); got != want {
		t.Fatalf("VolumeName() = %q, want %q", got, want)
	}

	other := Identity{HostUID: 1001, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}
	if identity.VolumeName() == other.VolumeName() {
		t.Fatalf("volumes for different host UIDs must not collide: %q", identity.VolumeName())
	}

	arm := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureARM64}}
	if identity.VolumeName() == arm.VolumeName() {
		t.Fatalf("volumes for different targets must not collide: %q", identity.VolumeName())
	}

	nextProtocol := Identity{HostUID: 1000, Protocol: 2, Target: Target{Architecture: ArchitectureAMD64}}
	if identity.VolumeName() == nextProtocol.VolumeName() {
		t.Fatalf("volumes for different protocol versions must not collide: %q", identity.VolumeName())
	}
}

func TestMaintenanceContainerNameIsDeterministicAndDistinctFromVolume(t *testing.T) {
	t.Parallel()
	identity := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}
	want := "codex-safe-codex-update-v1-1000-linux-amd64"
	if got := identity.MaintenanceContainerName(); got != want {
		t.Fatalf("MaintenanceContainerName() = %q, want %q", got, want)
	}
	if identity.MaintenanceContainerName() == identity.VolumeName() {
		t.Fatalf("the maintenance container name must not collide with the volume name")
	}
}
