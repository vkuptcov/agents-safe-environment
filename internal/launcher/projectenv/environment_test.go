package projectenv

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	loaded, err := loadConfig(root, ProjectConfig{Common: CommonConfig{
		PortableCommonConfig: PortableCommonConfig{DockerStorage: DockerStorageShared, Image: "other:image"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, config) {
		t.Fatalf("Load() = %#v, want %#v", loaded, config)
	}
	common, err := os.ReadFile(filepath.Join(contextPath, CommonConfigName))
	if err != nil || strings.Contains(string(common), "mounts") || !strings.Contains(string(common), "image = \"test:image\"") {
		t.Fatalf("common.toml = %q, %v; want portable values only", common, err)
	}
	local, err := os.ReadFile(filepath.Join(contextPath, ConfigName))
	if err != nil || strings.Contains(string(local), "image") || strings.Contains(string(local), "[codex]") ||
		!strings.Contains(string(local), "[[common.mounts]]") {
		t.Fatalf("config.toml = %q, %v; want host-specific lists only", local, err)
	}
	if data, err := os.ReadFile(filepath.Join(contextPath, ".gitignore")); err != nil || string(data) != "*\n!.gitignore\n!Dockerfile\n!common.toml\n" {
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
		CommonConfigName:     "[common]\nkeep_container = true\n",
		".gitignore":         "keep local rules\n",
	}
	for name, content := range files {
		writeFile(t, filepath.Join(contextPath, name), content)
	}
	for count := 0; count < 2; count++ {
		if _, err := Initialize(root, initializationConfig(t)); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range files {
		assertFile(t, filepath.Join(contextPath, name), want)
	}
}

func TestInitializeLazyHandlesEachFileIndependently(t *testing.T) {
	t.Parallel()
	const existingCommon = "[common]\nkeep_container = true\n"
	const existingLocal = "[[common.dependency_caches]]\nkind = \"uv\"\nsource = \"/keep/cache\"\n"
	for _, test := range []struct {
		name         string
		commonExists bool
		localExists  bool
	}{
		{name: "fresh"},
		{name: "common only", commonExists: true},
		{name: "local only", localExists: true},
		{name: "both", commonExists: true, localExists: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			contextPath := filepath.Join(root, Directory)
			if err := os.Mkdir(contextPath, 0o755); err != nil {
				t.Fatal(err)
			}
			if test.commonExists {
				writeFile(t, filepath.Join(contextPath, CommonConfigName), existingCommon)
			}
			if test.localExists {
				writeFile(t, filepath.Join(contextPath, ConfigName), existingLocal)
			}
			config := initializationConfig(t)
			commonCalls, localCalls := 0, 0
			result, err := InitializeLazy(root,
				func() (ProjectConfig, error) { commonCalls++; return config, nil },
				func() (ProjectConfig, error) { localCalls++; return config, nil })
			if err != nil {
				t.Fatal(err)
			}
			want := Initialization{Path: contextPath, CreatedCommon: !test.commonExists, CreatedConfig: !test.localExists}
			if result != want {
				t.Fatalf("InitializeLazy() = %#v, want %#v", result, want)
			}
			if wantCalls := boolCount(!test.commonExists); commonCalls != wantCalls {
				t.Fatalf("common provider calls = %d, want %d", commonCalls, wantCalls)
			}
			if wantCalls := boolCount(!test.localExists); localCalls != wantCalls {
				t.Fatalf("local provider calls = %d, want %d", localCalls, wantCalls)
			}
			if test.commonExists {
				assertFile(t, filepath.Join(contextPath, CommonConfigName), existingCommon)
			}
			if test.localExists {
				assertFile(t, filepath.Join(contextPath, ConfigName), existingLocal)
			}
		})
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path string, want string) {
	t.Helper()
	if data, err := os.ReadFile(path); err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
	}
}

func TestInitializeSerializesExplicitEmptyDependencyCaches(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := initializationConfig(t)
	config.Common.DependencyCaches = []DependencyCacheConfig{}
	contextPath, err := Initialize(root, config)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(contextPath, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "dependency_caches = []") {
		t.Fatalf("config.toml = %q, want explicit empty dependency-cache snapshot", data)
	}
}

func TestInitializeSerializesHostVirtualEnvironmentPolicy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := initializationConfig(t)
	config.Common.UseHostPythonVenv = false
	contextPath, err := Initialize(root, config)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(contextPath, CommonConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "use_host_python_venv = false") {
		t.Fatalf("common.toml = %q, want explicit host virtual-environment policy", data)
	}
}

func TestInitializeSerializesTmpfsMounts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := initializationConfig(t)
	config.Common.TmpfsMounts = []TmpfsMountConfig{{
		Target: filepath.Join(root, ".venv"), Mode: DefaultTmpfsMode, Comment: "project venv",
	}}
	contextPath, err := Initialize(root, config)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(contextPath, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "[[common.tmpfs_mounts]]") ||
		!strings.Contains(content, "target = \""+filepath.Join(root, ".venv")+"\"") ||
		!strings.Contains(content, "mode = \"1777\"") {
		t.Fatalf("config.toml = %q, want explicit tmpfs mount", data)
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
		PortableCommonConfig: PortableCommonConfig{DockerStorage: DockerStorageBranch, Image: "test:image"},
		HostCommonConfig:     HostCommonConfig{Mounts: []MountConfig{{Role: RoleAdditional, Source: source, Target: source}}},
	}}
}
