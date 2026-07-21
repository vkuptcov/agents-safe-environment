package codexinstall

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// digestPrefix is the only content-digest algorithm this store protocol records.
const digestPrefix = "sha256:"

// digestLength is the total length of a "sha256:<64 hex characters>" digest.
const digestLength = len(digestPrefix) + 64

// ReleaseManifest is the self-describing metadata every published release carries, so no separate
// mutable store metadata write is required after the atomic "current" replacement that publishes it.
type ReleaseManifest struct {
	Protocol   int
	Target     Target
	Version    string
	Entrypoint string
	Digest     string
}

// Validate checks that every manifest field is present and well-formed and that the manifest matches
// the identity a caller resolved for this Docker daemon, host UID, and store protocol. It performs no
// filesystem I/O; a caller that read the manifest from disk validates its JSON shape separately before
// calling this.
func (manifest ReleaseManifest) Validate(identity Identity) error {
	if manifest.Protocol != identity.Protocol {
		return fmt.Errorf(
			"release manifest protocol %d does not match store protocol %d", manifest.Protocol, identity.Protocol,
		)
	}
	if manifest.Target != identity.Target {
		return fmt.Errorf(
			"release manifest target %q does not match store target %q", manifest.Target, identity.Target,
		)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("release manifest version is required")
	}
	if strings.TrimSpace(manifest.Entrypoint) == "" {
		return fmt.Errorf("release manifest entrypoint is required")
	}
	if !strings.HasPrefix(manifest.Digest, digestPrefix) || len(manifest.Digest) != digestLength {
		return fmt.Errorf("release manifest digest %q is not a sha256 digest", manifest.Digest)
	}
	if _, err := hex.DecodeString(manifest.Digest[len(digestPrefix):]); err != nil {
		return fmt.Errorf("release manifest digest %q is not a sha256 digest", manifest.Digest)
	}
	return nil
}
