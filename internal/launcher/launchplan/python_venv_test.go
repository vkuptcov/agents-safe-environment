package launchplan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverPythonVirtualEnvironmentsFindsMarkersWithoutFollowingSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := filepath.Join(root, ".venv")
	second := filepath.Join(root, "service", ".venv-dev")
	hiddenGitVenv := filepath.Join(root, ".git", "ignored-venv")
	external := filepath.Join(t.TempDir(), "external-venv")
	for _, path := range []string{first, second, hiddenGitVenv, external} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, pythonVenvMarker), []byte("home = /usr/bin\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(external, filepath.Join(root, "linked-venv")); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverPythonVirtualEnvironments(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverPythonVirtualEnvironments() = %#v, want %#v", got, want)
	}
}

func TestDiscoverPythonVirtualEnvironmentsRejectsUnsafeMarkers(t *testing.T) {
	t.Parallel()
	t.Run("project root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, pythonVenvMarker), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := DiscoverPythonVirtualEnvironments(root); err == nil || !strings.Contains(err.Error(), "itself") {
			t.Fatalf("error = %v, want project-root rejection", err)
		}
	})
	t.Run("marker symlink", func(t *testing.T) {
		root := t.TempDir()
		environment := filepath.Join(root, ".venv")
		if err := os.Mkdir(environment, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing", filepath.Join(environment, pythonVenvMarker)); err != nil {
			t.Fatal(err)
		}
		if _, err := DiscoverPythonVirtualEnvironments(root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("error = %v, want non-regular marker rejection", err)
		}
	})
}

func TestRootPythonVenvReservationRecognizesRegularProjectMarkers(t *testing.T) {
	t.Parallel()
	for _, marker := range rootPythonProjectMarkers {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, marker), nil, 0o600); err != nil {
				t.Fatal(err)
			}

			got, found, err := rootPythonVenvReservation(root)
			if err != nil {
				t.Fatal(err)
			}
			want := TmpfsMount{
				Target: filepath.Join(root, ".venv"), Mode: "1777", CreateTarget: true,
			}
			if !found || !reflect.DeepEqual(got, want) {
				t.Fatalf("rootPythonVenvReservation() = %#v, %t, want %#v, true", got, found, want)
			}
		})
	}
}

func TestRootPythonVenvReservationIgnoresNonRegularProjectMarkers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "pyproject.toml")
	if err := os.WriteFile(external, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "pyproject.toml")); err != nil {
		t.Fatal(err)
	}

	if got, found, err := rootPythonVenvReservation(root); err != nil || found {
		t.Fatalf("rootPythonVenvReservation() = %#v, %t, %v, want no reservation", got, found, err)
	}
}

func TestRootPythonVenvReservationUsesExistingDirectoryWithoutMaterialization(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, ".venv")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	got, found, err := rootPythonVenvReservation(root)
	if err != nil {
		t.Fatal(err)
	}
	want := TmpfsMount{Target: target, Mode: "1777"}
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("rootPythonVenvReservation() = %#v, %t, want %#v, true", got, found, want)
	}
}

func TestRootPythonVenvReservationRejectsUnsafeTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".venv")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := rootPythonVenvReservation(root); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("rootPythonVenvReservation() error = %v, want unsafe-target rejection", err)
	}
}
