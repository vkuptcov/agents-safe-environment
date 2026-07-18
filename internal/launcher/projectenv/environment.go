// Package projectenv defines the project-owned Docker build context used by the launcher.
package projectenv

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	Directory       = ".agents-safe"
	DockerfileName  = "Dockerfile"
	ContractVersion = "1"

	ProjectImageLabel        = "codex-safe.project-image"
	ProjectKeyLabel          = "codex-safe.project-key"
	DefinitionLabel          = "codex-safe.project-definition"
	BaseImageIDLabel         = "codex-safe.base-image-id"
	ProjectImageLabelValue   = "true"
	EnvironmentLabelPrefix   = "sha256:"
	AbsentEnvironmentLabel   = "absent"
	OverrideEnvironmentLabel = "override"
)

// Definition is a validated immutable snapshot of a project build context.
type Definition struct {
	ContextPath    string
	DockerfilePath string
	Digest         string
}

// EnvironmentLabel returns the value carried by a session created from this definition.
func (definition Definition) EnvironmentLabel() string {
	return EnvironmentLabelPrefix + definition.Digest
}

// Discover returns nil when the project has no .agents-safe/Dockerfile. The project root must be
// canonical because it becomes part of the launch plan's trusted filesystem boundary.
func Discover(projectRoot string) (*Definition, error) {
	if !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot {
		return nil, fmt.Errorf("project root %q is not canonical", projectRoot)
	}
	contextPath := filepath.Join(projectRoot, Directory)
	info, err := os.Lstat(contextPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect project environment %q: %w", contextPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project environment %q is not a directory", contextPath)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("project environment %q is a symlink", contextPath)
	}

	dockerfilePath := filepath.Join(contextPath, DockerfileName)
	dockerfileInfo, err := os.Lstat(dockerfilePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect project Dockerfile %q: %w", dockerfilePath, err)
	}
	if !dockerfileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("project Dockerfile %q is not a regular file", dockerfilePath)
	}

	digest, err := digestContext(contextPath)
	if err != nil {
		return nil, err
	}
	return &Definition{ContextPath: contextPath, DockerfilePath: dockerfilePath, Digest: digest}, nil
}

func digestContext(contextPath string) (string, error) {
	var entries []string
	err := filepath.WalkDir(contextPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(contextPath, path)
		if err != nil {
			return fmt.Errorf("relativize project environment entry %q: %w", path, err)
		}
		if relative == "." {
			return nil
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("project environment entry %q escapes %q", path, contextPath)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("project environment entry %q is a symlink", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect project environment entry %q: %w", path, err)
		}
		if !info.Mode().IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("project environment entry %q is not a regular file or directory", path)
		}
		entries = append(entries, relative)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("validate project environment %q: %w", contextPath, err)
	}
	sort.Strings(entries)

	hash := sha256.New()
	for _, relative := range entries {
		path := filepath.Join(contextPath, relative)
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("reinspect project environment entry %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsDir() && !info.Mode().IsRegular()) {
			return "", fmt.Errorf("project environment entry %q changed while hashing", path)
		}
		entryType := "file"
		if info.IsDir() {
			entryType = "dir"
		}
		if _, err := fmt.Fprintf(hash, "%s\x00%s\x00%o\x00", relative, entryType, info.Mode().Perm()&0o111); err != nil {
			return "", fmt.Errorf("hash project environment entry %q: %w", path, err)
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return "", fmt.Errorf("open project environment entry %q: %w", path, err)
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", fmt.Errorf("hash project environment entry %q: %w", path, copyErr)
			}
			if closeErr != nil {
				return "", fmt.Errorf("close project environment entry %q: %w", path, closeErr)
			}
		}
		if _, err := hash.Write([]byte{0}); err != nil {
			return "", fmt.Errorf("hash project environment entry %q: %w", path, err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CacheKey combines a definition digest with the immutable base image and contract version.
func CacheKey(definitionDigest string, baseImageID string) (string, error) {
	if err := validateDigest("definition digest", definitionDigest); err != nil {
		return "", err
	}
	if err := validateImageID(baseImageID); err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s", definitionDigest, baseImageID, ContractVersion)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// LocalImageName creates a Docker-safe deterministic local tag for one project and cache key.
func LocalImageName(projectKey string, cacheKey string) (string, error) {
	if strings.TrimSpace(projectKey) == "" || strings.ContainsAny(projectKey, " \t\r\n/@:") {
		return "", fmt.Errorf("invalid project key %q", projectKey)
	}
	if err := validateDigest("cache key", cacheKey); err != nil {
		return "", err
	}
	return "codex-safe-project-" + projectKey + ":" + cacheKey[:16], nil
}

func validateDigest(name string, value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	return nil
}

func validateImageID(imageID string) error {
	digest, found := strings.CutPrefix(imageID, "sha256:")
	if !found {
		return fmt.Errorf("invalid base image ID %q", imageID)
	}
	return validateDigest("base image ID", digest)
}
