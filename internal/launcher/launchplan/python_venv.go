package launchplan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const pythonVenvMarker = "pyvenv.cfg"

// DiscoverPythonVirtualEnvironments returns existing project-local Python virtual environments in
// deterministic path order. A regular pyvenv.cfg identifies an environment; symlinks are never followed.
func DiscoverPythonVirtualEnvironments(projectRoot string) ([]string, error) {
	if err := ValidateMountPath("project root", projectRoot); err != nil {
		return nil, err
	}
	var environments []string
	err := filepath.WalkDir(projectRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect project path %q while discovering Python virtual environments: %w", path, walkErr)
		}
		if !entry.IsDir() {
			return nil
		}
		if path != projectRoot && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		marker := filepath.Join(path, pythonVenvMarker)
		info, err := os.Lstat(marker)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect Python virtual environment marker %q: %w", marker, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Python virtual environment marker %q is not a regular file", marker)
		}
		if path == projectRoot {
			return fmt.Errorf("project root %q is itself a Python virtual environment and cannot be masked", projectRoot)
		}
		environments = append(environments, path)
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return environments, nil
}
