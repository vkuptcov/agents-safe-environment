package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
)

const projectKeyHexLength = 24

// ProjectKey returns the stable project-and-user key used in the Docker name.
// Full identity remains in labels and is always validated before reuse.
func ProjectKey(hostUID int, projectRoot string) (string, error) {
	if hostUID < 0 {
		return "", fmt.Errorf("invalid host UID %d", hostUID)
	}
	if err := validateMountPath("project root", projectRoot); err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(strconv.Itoa(hostUID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(projectRoot))
	return hex.EncodeToString(hash.Sum(nil))[:projectKeyHexLength], nil
}

// ProjectContainerName returns the daemon-global deterministic Docker name for
// one canonical project root and host UID.
func ProjectContainerName(hostUID int, projectRoot string) (string, error) {
	key, err := ProjectKey(hostUID, projectRoot)
	if err != nil {
		return "", err
	}
	return "codex-safe-" + key, nil
}
