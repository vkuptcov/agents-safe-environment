package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

const projectKeyHexLength = 24

// ProjectKey returns the stable project-and-user key for an already-validated
// host identity and canonical project root.
func ProjectKey(hostUID int, projectRoot string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(strconv.Itoa(hostUID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(projectRoot))
	return hex.EncodeToString(hash.Sum(nil))[:projectKeyHexLength]
}

// ProjectContainerName returns the daemon-global deterministic Docker name for
// one already-validated canonical project root and host UID.
func ProjectContainerName(hostUID int, projectRoot string) string {
	return "codex-safe-" + ProjectKey(hostUID, projectRoot)
}
