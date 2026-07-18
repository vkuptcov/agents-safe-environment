package projectenv

import (
	"os"
	"path/filepath"
	"strings"
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

func TestCreateSampleCreatesInactiveTemplate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	samplePath, err := CreateSample(root)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, Directory, DockerfileSampleName)
	if samplePath != wantPath {
		t.Fatalf("CreateSample() path = %q, want %q", samplePath, wantPath)
	}
	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != DockerfileSampleContent {
		t.Fatalf("sample content = %q, want %q", content, DockerfileSampleContent)
	}
	for _, required := range []string{"ARG AGENTS_SAFE_BASE", "FROM ${AGENTS_SAFE_BASE}"} {
		if !strings.Contains(DockerfileSampleContent, required) {
			t.Errorf("embedded sample does not contain %q", required)
		}
	}
	if _, err := os.Stat(filepath.Join(root, Directory, DockerfileName)); !os.IsNotExist(err) {
		t.Fatalf("active Dockerfile stat error = %v, want not exist", err)
	}
	if strings.Contains(DockerfileSampleContent, "\n    AS go-toolchain") {
		t.Fatal("multi-line example contains an uncommented continuation")
	}
}

func TestCreateSampleDoesNotOverwriteExistingFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	contextPath := filepath.Join(root, Directory)
	if err := os.Mkdir(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	samplePath := filepath.Join(contextPath, DockerfileSampleName)
	if err := os.WriteFile(samplePath, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateSample(root); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("CreateSample() error = %v, want already exists", err)
	}
	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep me\n" {
		t.Fatalf("existing sample = %q, want preserved content", content)
	}
}

func TestCreateSampleRejectsSymlinkContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	actual := t.TempDir()
	if err := os.Symlink(actual, filepath.Join(root, Directory)); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateSample(root); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("CreateSample() error = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(actual, DockerfileSampleName)); !os.IsNotExist(err) {
		t.Fatalf("sample through symlink stat error = %v, want not exist", err)
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
