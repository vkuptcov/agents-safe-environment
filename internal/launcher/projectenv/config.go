package projectenv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type configFile struct {
	Mounts []string `toml:"mounts"`
}

// LoadMounts returns canonical additional host directories from the local project configuration.
func LoadMounts(projectRoot string) ([]string, error) {
	contextPath, exists, err := inspectContextDirectory(projectRoot)
	if err != nil || !exists {
		return nil, err
	}

	configPath := filepath.Join(contextPath, ConfigName)
	info, err := os.Lstat(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect project environment config %q: %w", configPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("project environment config %q is not a regular file", configPath)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read project environment config %q: %w", configPath, err)
	}

	var decoded configFile
	metadata, err := toml.Decode(string(data), &decoded)
	if err != nil {
		return nil, fmt.Errorf("parse project environment config %q: %w", configPath, err)
	}
	if unknown := metadata.Undecoded(); len(unknown) != 0 {
		return nil, fmt.Errorf("project environment config %q contains unknown setting %q", configPath, unknown[0])
	}

	mounts := make([]string, 0, len(decoded.Mounts))
	seen := make(map[string]bool, len(decoded.Mounts))
	for _, requested := range decoded.Mounts {
		if requested == "" || strings.TrimSpace(requested) != requested || !filepath.IsAbs(requested) {
			return nil, fmt.Errorf("configured mount %q must be a literal absolute path", requested)
		}
		canonical, err := filepath.EvalSymlinks(requested)
		if err != nil {
			return nil, fmt.Errorf("resolve configured mount %q: %w", requested, err)
		}
		canonical = filepath.Clean(canonical)
		if canonical == string(filepath.Separator) {
			return nil, errors.New("configured mount cannot be the filesystem root")
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return nil, fmt.Errorf("inspect configured mount %q: %w", canonical, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("configured mount %q is not a directory", canonical)
		}
		if !seen[canonical] {
			seen[canonical] = true
			mounts = append(mounts, canonical)
		}
	}
	return mounts, nil
}
