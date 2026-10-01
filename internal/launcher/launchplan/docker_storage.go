package launchplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// dockerStorageVolume keeps names readable while hashing canonical project paths and host UIDs to avoid
// collisions introduced by normalization and truncation. Linked worktrees use the primary checkout identity.
func dockerStorageVolume(project gitproject.Project, mode string, hostUID int) (string, error) {
	if err := projectenv.ValidateDockerStorage(mode); err != nil {
		return "", err
	}
	const prefix = "agents-safe-docker-"
	if mode == "shared" {
		return prefix + "shared", nil
	}
	projectName := filepath.Base(project.PrimaryRoot)
	identity := strconv.Itoa(hostUID) + "\x00" + project.PrimaryRoot
	readable := storageNamePart(projectName, 15)
	if mode == "branch" {
		if project.Branch == "" {
			return "", fmt.Errorf("Git branch identity is required for branch Docker storage")
		}
		identity += "\x00" + project.Branch
		readable += "-" + storageNamePart(project.Branch, 20)
	}
	digest := sha256.Sum256([]byte(identity))
	return prefix + readable + "-" + hex.EncodeToString(digest[:6]), nil
}

func storageNamePart(value string, limit int) string {
	var part strings.Builder
	for _, character := range strings.ToLower(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			part.WriteRune(character)
		} else {
			part.WriteByte('-')
		}
		if part.Len() == limit {
			break
		}
	}
	result := strings.Trim(part.String(), "-")
	if result == "" {
		return "unnamed"
	}
	return result
}
