package projectenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAbsentDockerfile(t *testing.T) {
	t.Parallel()
	definition, err := Discover(t.TempDir())
	if err != nil || definition != nil {
		t.Fatalf("Discover() = (%#v, %v), want (nil, nil)", definition, err)
	}
}

func TestDiscoverReturnsFixedBuildContext(t *testing.T) {
	t.Parallel()
	root := writeContext(t)
	definition, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	wantContext := filepath.Join(root, Directory)
	if definition == nil || definition.ContextPath != wantContext {
		t.Fatalf("Discover() context = %#v, want %q", definition, wantContext)
	}
	if want := filepath.Join(wantContext, DockerfileName); definition.DockerfilePath != want {
		t.Fatalf("Discover() Dockerfile = %q, want %q", definition.DockerfilePath, want)
	}
}

func TestDiscoverRejectsSymlinkBoundaries(t *testing.T) {
	t.Parallel()
	t.Run("context", func(t *testing.T) {
		root := t.TempDir()
		actual := filepath.Join(root, "actual")
		if err := os.Mkdir(actual, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(actual, filepath.Join(root, Directory)); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(root); err == nil {
			t.Fatal("Discover() accepted a symlink build context")
		}
	})
	t.Run("Dockerfile", func(t *testing.T) {
		root := t.TempDir()
		contextPath := filepath.Join(root, Directory)
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing", filepath.Join(contextPath, DockerfileName)); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(root); err == nil {
			t.Fatal("Discover() accepted a symlink Dockerfile")
		}
	})
}

func TestLocalImageName(t *testing.T) {
	t.Parallel()
	name, err := LocalImageName("project-key")
	if err != nil {
		t.Fatal(err)
	}
	if want := "codex-safe-project-project-key:local"; name != want {
		t.Fatalf("name = %q, want %q", name, want)
	}
}

func writeContext(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	contextPath := filepath.Join(root, Directory)
	if err := os.Mkdir(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextPath, DockerfileName), []byte("FROM base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
