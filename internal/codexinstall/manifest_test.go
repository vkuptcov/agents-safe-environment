package codexinstall

import (
	"strings"
	"testing"
)

func validManifest(identity Identity) ReleaseManifest {
	return ReleaseManifest{
		Protocol:   identity.Protocol,
		Target:     identity.Target,
		Version:    "0.150.0",
		Entrypoint: "codex",
		Digest:     "sha256:" + strings.Repeat("a", 64),
	}
}

func TestReleaseManifestValidateAcceptsWellFormedManifest(t *testing.T) {
	t.Parallel()
	identity := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}
	if err := validManifest(identity).Validate(identity); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestReleaseManifestValidateRejectsMismatch(t *testing.T) {
	t.Parallel()
	identity := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}

	wrongProtocol := validManifest(identity)
	wrongProtocol.Protocol = 2
	if err := wrongProtocol.Validate(identity); err == nil {
		t.Fatal("Validate() must reject a mismatched protocol")
	}

	wrongTarget := validManifest(identity)
	wrongTarget.Target = Target{Architecture: ArchitectureARM64}
	if err := wrongTarget.Validate(identity); err == nil {
		t.Fatal("Validate() must reject a mismatched target")
	}
}

func TestReleaseManifestValidateRejectsMissingFields(t *testing.T) {
	t.Parallel()
	identity := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}

	noVersion := validManifest(identity)
	noVersion.Version = ""
	if err := noVersion.Validate(identity); err == nil {
		t.Fatal("Validate() must reject an empty version")
	}

	noEntrypoint := validManifest(identity)
	noEntrypoint.Entrypoint = ""
	if err := noEntrypoint.Validate(identity); err == nil {
		t.Fatal("Validate() must reject an empty entrypoint")
	}
}

func TestReleaseManifestValidateRejectsMalformedDigest(t *testing.T) {
	t.Parallel()
	identity := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}
	for _, digest := range []string{
		"",
		"not-a-digest",
		"sha256:" + strings.Repeat("a", 63),
		"sha1:" + strings.Repeat("a", 40),
		// Correct prefix and length, but the body is not hex: the digest is a content-integrity
		// value, so a well-shaped-but-invalid one must still be rejected.
		"sha256:" + strings.Repeat("z", 64),
	} {
		manifest := validManifest(identity)
		manifest.Digest = digest
		if err := manifest.Validate(identity); err == nil {
			t.Fatalf("Validate() must reject digest %q", digest)
		}
	}
}
