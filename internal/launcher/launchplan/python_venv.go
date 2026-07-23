package launchplan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const pythonVenvMarker = "pyvenv.cfg"

var rootPythonProjectMarkers = []string{
	"pyproject.toml",
	"setup.py",
	"setup.cfg",
	"requirements.txt",
	"Pipfile",
	"uv.lock",
	"poetry.lock",
	"pdm.lock",
	"tox.ini",
	"pytest.ini",
	".python-version",
}

// rootPythonVenvReservation selects the conventional root .venv before it has a pyvenv.cfg marker.
// Only regular root project markers count, so repository-controlled symlinks cannot trigger host writes.
func rootPythonVenvReservation(projectRoot string) (TmpfsMount, bool, error) {
	pythonProject, err := hasRootPythonProjectMarker(projectRoot)
	if err != nil || !pythonProject {
		return TmpfsMount{}, false, err
	}

	target := filepath.Join(projectRoot, ".venv")
	info, err := os.Lstat(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return TmpfsMount{
			Target: target, Mode: projectenv.DefaultTmpfsMode, CreateTarget: true, Owned: true,
		}, true, nil
	case err != nil:
		return TmpfsMount{}, false, fmt.Errorf("inspect proactive Python virtual environment target %q: %w", target, err)
	case !info.IsDir():
		return TmpfsMount{}, false, fmt.Errorf(
			"proactive Python virtual environment target %q exists but is not a directory",
			target,
		)
	default:
		return TmpfsMount{Target: target, Mode: projectenv.DefaultTmpfsMode, Owned: true}, true, nil
	}
}

func hasRootPythonProjectMarker(projectRoot string) (bool, error) {
	for _, name := range rootPythonProjectMarkers {
		path := filepath.Join(projectRoot, name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("inspect Python project marker %q: %w", path, err)
		}
		if info.Mode().IsRegular() {
			return true, nil
		}
	}
	return false, nil
}

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
