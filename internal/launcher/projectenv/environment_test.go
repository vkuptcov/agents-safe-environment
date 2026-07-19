package projectenv

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInitializeCreatesTypedLocalFilesWithoutTouchingRootIgnore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rootIgnore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(rootIgnore, []byte("keep-root-rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := initializationConfig(t)

	contextPath, err := Initialize(root, config)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, Directory); contextPath != want {
		t.Fatalf("Initialize() path = %q, want %q", contextPath, want)
	}
	if data, err := os.ReadFile(filepath.Join(contextPath, DockerfileSampleName)); err != nil || string(data) != dockerfileSampleContent {
		t.Fatalf("Dockerfile sample = %q, %v", data, err)
	}
	loaded, err := Load(root, config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, config) {
		t.Fatalf("Load() = %#v, want %#v", loaded, config)
	}
	if data, err := os.ReadFile(filepath.Join(contextPath, ".gitignore")); err != nil || string(data) != localIgnoreContent {
		t.Fatalf("local ignore = %q, %v", data, err)
	}
	if data, err := os.ReadFile(rootIgnore); err != nil || string(data) != "keep-root-rules\n" {
		t.Fatalf("root ignore = %q, %v", data, err)
	}
}

func TestInitializePreservesExistingLocalFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	contextPath := filepath.Join(root, Directory)
	if err := os.Mkdir(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		DockerfileSampleName: "keep sample\n",
		ConfigName:           "[common]\nimage = \"keep:image\"\n",
		".gitignore":         "keep local rules\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(contextPath, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for count := 0; count < 2; count++ {
		if _, err := Initialize(root, initializationConfig(t)); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(contextPath, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v; want %q", name, data, err, want)
		}
	}
}

func TestInitializeRejectsSymlinkLocalFilesWithoutReadingRootIgnore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	contextPath := filepath.Join(root, Directory)
	if err := os.Mkdir(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(contextPath, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(root, initializationConfig(t)); err == nil {
		t.Fatal("Initialize() accepted a local ignore symlink")
	}
}

func TestInitializeRejectsSymlinkContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, Directory)); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(root, initializationConfig(t)); err == nil {
		t.Fatal("Initialize() accepted a symlink context")
	}
}

func initializationConfig(t *testing.T) ProjectConfig {
	t.Helper()
	source := t.TempDir()
	return ProjectConfig{Common: CommonConfig{
		Image:  "test:image",
		Mounts: []MountConfig{{Role: RoleAdditional, Source: source, Target: source}},
	}}
}
