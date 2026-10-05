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

// DockerDataRoot is the nested daemon's data directory. The launcher mounts persistent storage here and
// recognizes an existing session's storage by the same target, so both must name one path.
const DockerDataRoot = "/var/lib/docker"

// DockerStorageFormat versions the nested daemon's data-root layout and suffixes every volume name, so a new format
// starts on fresh volumes while older images keep theirs. Unsuffixed volumes are format 1 (containerd image store).
const DockerStorageFormat = 2

// DockerStorage is the resolved nested Docker storage selection for one launch. It is immutable for the
// lifetime of a container: creation mounts Volume, and a saved container keeps its creation-time mount.
type DockerStorage struct {
	Mode   projectenv.DockerStorageMode
	Volume string
	// ProjectRoot and Branch are the complete unhashed identities Volume is derived from. The launcher
	// records them on the volume so retained storage stays attributable. Host-wide storage has neither,
	// which is what makes it unowned.
	ProjectRoot string
	Branch      string
}

// Describe explains a storage selection that no longer matches an existing container's mount.
func (storage DockerStorage) Describe(existingVolume string) string {
	const hint = "nested Docker storage volume changed from %q to %q (requested scope %q); " +
		"branch switches change branch-scoped storage, and a newer image's storage format changes every scope; " +
		"--docker-storage=project keeps storage across branches for new containers"
	return fmt.Sprintf(hint, existingVolume, storage.Volume, storage.Mode)
}

// resolveDockerStorage keeps names readable while hashing canonical project paths and host UIDs to avoid
// collisions introduced by normalization and truncation. Linked worktrees use the primary checkout identity.
func resolveDockerStorage(
	project gitproject.Project,
	mode projectenv.DockerStorageMode,
	hostUID int,
) (DockerStorage, error) {
	const prefix = "agents-safe-docker-"
	suffix := "-v" + strconv.Itoa(DockerStorageFormat)
	storage := DockerStorage{Mode: mode}
	switch mode {
	case projectenv.DockerStorageShared:
		storage.Volume = prefix + "shared" + suffix
		return storage, nil
	case projectenv.DockerStorageBranch:
		if project.Branch == "" {
			return DockerStorage{}, fmt.Errorf("Git branch identity is required for branch Docker storage")
		}
		storage.Branch = project.Branch
	case projectenv.DockerStorageProject:
	default:
		return DockerStorage{}, fmt.Errorf("unsupported nested Docker storage scope %q", mode)
	}
	storage.ProjectRoot = project.PrimaryRoot
	identity := strconv.Itoa(hostUID) + "\x00" + project.PrimaryRoot
	readable := storageNamePart(filepath.Base(project.PrimaryRoot), 15)
	if storage.Branch != "" {
		identity += "\x00" + storage.Branch
		readable += "-" + storageNamePart(storage.Branch, 20)
	}
	digest := sha256.Sum256([]byte(identity))
	storage.Volume = prefix + readable + "-" + hex.EncodeToString(digest[:6]) + suffix
	return storage, nil
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
