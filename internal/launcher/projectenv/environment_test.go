package projectenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAbsentDockerfile(t *testing.T) {
	t.Parallel()
	contextPath, err := Discover(t.TempDir())
	if err != nil || contextPath != "" {
		t.Fatalf("Discover() = (%q, %v), want empty path and nil error", contextPath, err)
	}
}

func TestDiscoverReturnsFixedBuildContext(t *testing.T) {
	t.Parallel()
	root := writeContext(t)
	contextPath, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	wantContext := filepath.Join(root, Directory)
	if contextPath != wantContext {
		t.Fatalf("Discover() context = %q, want %q", contextPath, wantContext)
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
	name := LocalImageName("project-key")
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
