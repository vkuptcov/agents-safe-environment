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

func TestInitializeCreatesLocalFilesAndGitignore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	contextPath, err := Initialize(root)
	if err != nil {
		t.Fatal(err)
	}
	wantContext := filepath.Join(root, Directory)
	if contextPath != wantContext {
		t.Fatalf("Initialize() path = %q, want %q", contextPath, wantContext)
	}
	sample, err := os.ReadFile(filepath.Join(contextPath, DockerfileSampleName))
	if err != nil {
		t.Fatal(err)
	}
	if string(sample) != dockerfileSampleContent {
		t.Fatalf("sample content = %q, want %q", sample, dockerfileSampleContent)
	}
	for _, required := range []string{"ARG AGENTS_SAFE_BASE", "FROM ${AGENTS_SAFE_BASE}"} {
		if !strings.Contains(dockerfileSampleContent, required) {
			t.Errorf("embedded sample does not contain %q", required)
		}
	}
	config, err := os.ReadFile(filepath.Join(contextPath, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != configSampleContent || !strings.Contains(string(config), "mounts = []") {
		t.Fatalf("config content = %q, want embedded empty mount config", config)
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range localIgnoreRules {
		if !strings.Contains(string(ignore), required) {
			t.Errorf(".gitignore = %q, want %q", ignore, required)
		}
	}
	if _, err := os.Stat(filepath.Join(contextPath, DockerfileName)); !os.IsNotExist(err) {
		t.Fatalf("active Dockerfile stat error = %v, want not exist", err)
	}
	if strings.Contains(dockerfileSampleContent, "\n    AS go-toolchain") {
		t.Fatal("multi-line example contains an uncommented continuation")
	}
}

func TestInitializePreservesExistingFilesAndIsIdempotent(t *testing.T) {
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
	configPath := filepath.Join(contextPath, ConfigName)
	if err := os.WriteFile(configPath, []byte("mounts = [] # keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ignorePrefix := "keep-rule\n" + localIgnoreRules[0]
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(ignorePrefix), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Initialize(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(root); err != nil {
		t.Fatal(err)
	}
	sample, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(sample) != "keep me\n" {
		t.Fatalf("existing sample = %q, want preserved content", sample)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != "mounts = [] # keep\n" {
		t.Fatalf("existing config = %q, want preserved content", config)
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ignore), "keep-rule") {
		t.Errorf(".gitignore = %q, want preserved unrelated rule", ignore)
	}
	for _, rule := range localIgnoreRules {
		if count := strings.Count(string(ignore), rule); count != 1 {
			t.Errorf(".gitignore contains %q %d times, want once: %q", rule, count, ignore)
		}
	}
}

func TestInitializeRejectsSymlinkGitignore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "ignore")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}

	if _, err := Initialize(root); err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("Initialize() error = %v, want symlink .gitignore rejection", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep\n" {
		t.Fatalf("symlink target changed to %q", content)
	}
}

func TestInitializeRejectsSymlinkContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	actual := t.TempDir()
	if err := os.Symlink(actual, filepath.Join(root, Directory)); err != nil {
		t.Fatal(err)
	}

	if _, err := Initialize(root); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("Initialize() error = %v, want symlink rejection", err)
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
