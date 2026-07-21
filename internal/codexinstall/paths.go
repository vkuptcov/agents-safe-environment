package codexinstall

import (
	"fmt"
	"path"
	"strings"
)

// Store-relative path layout. These names are relative to the installation volume's root, which a
// session mount shadows at "<container Codex home>/packages/standalone".
const (
	// CurrentEntryName is the store's live pointer: a symlink to the currently selected release
	// directory under ReleasesDirName.
	CurrentEntryName = "current"
	// ReleasesDirName holds every immutable published release directory.
	ReleasesDirName = "releases"
)

// ReleaseDirName returns one release's immutable directory name: "<version>-linux-<architecture>".
func ReleaseDirName(version string, target Target) string {
	return version + "-" + target.String()
}

// ReleasePath returns one release's store-relative path: "releases/<version>-linux-<architecture>".
func ReleasePath(version string, target Target) string {
	return path.Join(ReleasesDirName, ReleaseDirName(version, target))
}

// WithinStore resolves a store-relative candidate path against the store root and rejects any result
// that would escape the store, such as a "current" symlink target or a manifest field containing "..".
//
// It is pure path algebra: it never touches the filesystem, so it cannot itself follow a symlink or
// detect one planted after the check. A filesystem-aware caller (internal/container, Phase 3) must
// still recheck the resolved path before trusting it, but no caller can skip this shape check first.
func WithinStore(storeRoot string, candidate string) (string, error) {
	if strings.TrimSpace(storeRoot) == "" {
		return "", fmt.Errorf("store root is required")
	}
	if strings.TrimSpace(candidate) == "" {
		return "", fmt.Errorf("candidate store path is required")
	}
	cleanRoot := path.Clean(storeRoot)
	resolved := path.Clean(path.Join(cleanRoot, candidate))
	if resolved != cleanRoot && !strings.HasPrefix(resolved, cleanRoot+"/") {
		return "", fmt.Errorf("path %q escapes installation store %q", candidate, storeRoot)
	}
	return resolved, nil
}
