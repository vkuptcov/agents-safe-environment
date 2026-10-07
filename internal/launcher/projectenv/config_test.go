package projectenv

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLoadTypedConfigOverlaysPresentValues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	defaults := typedDefaults(t)
	writeConfig(t, root, `
[common]
image = "configured:image"
no_host_mcp = false
use_host_python_venv = true
keep_container = true

[codex]
arguments = ["exec", "--model", "gpt-5"]

[claude]
arguments = ["--model", "opus"]
`)

	config, err := loadConfig(root, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if config.Common.Image != "configured:image" {
		t.Errorf("image = %q, want configured value", config.Common.Image)
	}
	if config.Common.DockerStorage != DockerStorageBranch {
		t.Error("omitted DockerStorage must retain branch default")
	}
	if config.Common.NoHostMCP {
		t.Error("no_host_mcp = true, want explicit false")
	}
	if !config.Common.UseHostPythonVenv {
		t.Error("use_host_python_venv = false, want explicit true")
	}
	if !config.Common.KeepContainer {
		t.Error("keep_container = false, want explicit true")
	}
	if !reflect.DeepEqual(config.Common.Mounts, defaults.Common.Mounts) {
		t.Errorf("mounts = %#v, want omitted default %#v", config.Common.Mounts, defaults.Common.Mounts)
	}
	if !reflect.DeepEqual(config.Common.TmpfsMounts, defaults.Common.TmpfsMounts) {
		t.Errorf("tmpfs_mounts = %#v, want omitted default %#v", config.Common.TmpfsMounts, defaults.Common.TmpfsMounts)
	}
	if want := []string{"exec", "--model", "gpt-5"}; !reflect.DeepEqual(config.Codex.Arguments, want) {
		t.Errorf("arguments = %#v, want %#v", config.Codex.Arguments, want)
	}
	if want := []string{"--model", "opus"}; !reflect.DeepEqual(config.Claude.Arguments, want) {
		t.Errorf("Claude arguments = %#v, want %#v", config.Claude.Arguments, want)
	}
}

func TestLoadTypedConfigReplacesTmpfsMounts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configured := filepath.Join(root, "service", ".venv")
	defaults := typedDefaults(t)
	writeConfig(t, root, "[[common.tmpfs_mounts]]\ntarget = \""+configured+"\"\nmode = \"0755\"\ncomment = \"service environment\"\n")

	config, err := loadConfig(root, defaults)
	if err != nil {
		t.Fatal(err)
	}
	want := []TmpfsMountConfig{{Target: configured, Mode: "0755", Comment: "service environment"}}
	if !reflect.DeepEqual(config.Common.TmpfsMounts, want) {
		t.Fatalf("tmpfs_mounts = %#v, want %#v", config.Common.TmpfsMounts, want)
	}
	config.Common.TmpfsMounts[0].Mode = "0700"
	if defaults.Common.TmpfsMounts[0].Mode == "0700" {
		t.Fatal("Load() mutated default tmpfs mounts")
	}
}

func TestLoadTypedConfigReplacesMountsAndClonesDefaults(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	defaults := typedDefaults(t)
	writeConfig(t, root, `
[common]
mounts = []
`)

	config, err := loadConfig(root, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if config.Common.Mounts == nil || len(config.Common.Mounts) != 0 {
		t.Fatalf("mounts = %#v, want present empty list", config.Common.Mounts)
	}
	config.Codex.Arguments[0] = "changed"
	if defaults.Codex.Arguments[0] == "changed" {
		t.Fatal("Load() mutated default arguments")
	}
	config.Claude.Arguments[0] = "changed"
	if defaults.Claude.Arguments[0] == "changed" {
		t.Fatal("Load() mutated default Claude arguments")
	}
}

func TestLoadTypedConfigReplacesDependencyCachesAndRejectsUnsupportedEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	defaults := typedDefaults(t)
	defaults.Common.DependencyCaches = []DependencyCacheConfig{{Kind: DependencyCacheGoBuild, Source: cache}}
	writeConfig(t, root, "[common]\ndependency_caches = []\n")
	config, err := loadConfig(root, defaults)
	if err != nil || len(config.Common.DependencyCaches) != 0 {
		t.Fatalf("Load() = %#v, %v", config.Common.DependencyCaches, err)
	}

	writeConfig(t, root, "[[common.dependency_caches]]\nkind = \"uv\"\nsource = \""+cache+"\"\n")
	config, err = loadConfig(root, defaults)
	if err != nil || !reflect.DeepEqual(config.Common.DependencyCaches, []DependencyCacheConfig{{
		Kind: DependencyCacheUV, Source: cache,
	}}) {
		t.Fatalf("Load() uv cache = %#v, %v", config.Common.DependencyCaches, err)
	}

	for _, content := range []string{
		"[[common.dependency_caches]]\nkind = \"maven\"\nsource = \"" + cache + "\"\n",
		"[[common.dependency_caches]]\nkind = \"go_build\"\nsource = \"" + cache + "\"\nmode = \"shared_rw\"\n",
		"[[common.dependency_caches]]\nkind = \"go_build\"\nsource = \"" + cache + "\"\n[[common.dependency_caches]]\nkind = \"go_build\"\nsource = \"" + cache + "\"\n",
	} {
		writeConfig(t, root, content)
		if _, err := loadConfig(root, defaults); err == nil {
			t.Fatalf("Load() accepted %q", content)
		}
	}
}

func TestLoadTypedConfigRejectsUnknownAndInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "unknown nested key",
			content: `
[common]
unknown = true
`,
			want: "unknown setting",
		},
		{
			name: "unsafe argument",
			content: `
[codex]
arguments = ["line\nbreak"]
`,
			want: "safe argv element",
		},
		{
			name: "unsafe Claude argument",
			content: `
[claude]
arguments = ["line\nbreak"]
`,
			want: "safe argv element",
		},
		{
			name: "malformed channel",
			content: `
[[common.mounts]]
role = "host_mcp_channel"
source = "runtime://unexpected"
target = "/run/agents-safe-host-mcp"
read_only = false
`,
			want: "invalid host MCP channel mount",
		},
		{
			name: "unsafe tmpfs mode",
			content: `
[[common.tmpfs_mounts]]
target = "/project/.venv"
mode = "1888"
`,
			want: "octal mode",
		},
		{
			name: "tmpfs target with option delimiter",
			content: `
[[common.tmpfs_mounts]]
target = "/project/venv:unsafe"
mode = "1777"
`,
			want: "Docker --tmpfs",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, test.content)
			_, err := loadConfig(root, typedDefaults(t))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestEncodeTypedConfigIsDeterministic(t *testing.T) {
	t.Parallel()
	config := typedDefaults(t)
	config.Common.DependencyCaches = []DependencyCacheConfig{}
	var first bytes.Buffer
	if err := Encode(config, &first); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := Encode(config, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("Encode() differs across calls:\n%s\n---\n%s", first.String(), second.String())
	}
	if !strings.Contains(first.String(), "[common]") || !strings.Contains(first.String(), "[[common.mounts]]") {
		t.Fatalf("Encode() = %q, want typed sections", first.String())
	}
	if !strings.Contains(first.String(), "dependency_caches = []") {
		t.Fatalf("Encode() = %q, want explicit empty dependency-cache snapshot", first.String())
	}
	if !strings.Contains(first.String(), "use_host_python_venv = false") {
		t.Fatalf("Encode() = %q, want explicit host virtual-environment policy", first.String())
	}
	if !strings.Contains(first.String(), "[[common.tmpfs_mounts]]") {
		t.Fatalf("Encode() = %q, want explicit tmpfs mount snapshot", first.String())
	}
}

func typedDefaults(t *testing.T) ProjectConfig {
	t.Helper()
	source := t.TempDir()
	return ProjectConfig{
		Common: CommonConfig{
			PortableCommonConfig: PortableCommonConfig{
				DockerStorage: DockerStorageBranch,
				Image:         "default:image",
				NoHostMCP:     true,
			},
			HostCommonConfig: HostCommonConfig{
				Mounts: []MountConfig{{Role: RoleAdditional, Source: source, Target: source}},
				TmpfsMounts: []TmpfsMountConfig{{
					Target: filepath.Join(source, ".venv"), Mode: DefaultTmpfsMode,
				}},
			},
		},
		Codex:  CodexConfig{Arguments: []string{"--sandbox", "danger-full-access"}},
		Claude: ClaudeConfig{Arguments: []string{"--permission-mode", "auto"}},
	}
}

func writeConfig(t *testing.T, root string, content string) {
	t.Helper()
	writeLayer(t, root, ConfigName, content)
}

func TestLoadDockerStorageModes(t *testing.T) {
	for _, mode := range append(slices.Clone(DockerStorageModeOrder), "", "unknown") {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, "[common]\ndocker_storage = \""+string(mode)+"\"\n")
			config, err := loadConfig(root, typedDefaults(t))
			if slices.Contains(DockerStorageModeOrder, mode) {
				if err != nil || config.Common.DockerStorage != mode {
					t.Fatalf("Load = %#v, %v", config, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid mode accepted")
			}
		})
	}
}

func TestLoadLayersAppliesCommonThenLocalConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	defaults := typedDefaults(t)
	writeCommonConfig(t, root, `
[common]
docker_storage = "project"
keep_container = true
image = "common:image"

[claude]
arguments = ["--model", "opus"]
`)
	writeConfig(t, root, `
[common]
image = "local:image"
keep_container = true
`)

	config, shadowed, err := LoadLayers(root, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if config.Common.DockerStorage != DockerStorageProject {
		t.Errorf("docker_storage = %q, want common.toml value", config.Common.DockerStorage)
	}
	if config.Common.Image != "local:image" {
		t.Errorf("image = %q, want config.toml override", config.Common.Image)
	}
	if !config.Common.KeepContainer {
		t.Error("keep_container = false, want true")
	}
	if want := []string{"--model", "opus"}; !reflect.DeepEqual(config.Claude.Arguments, want) {
		t.Errorf("Claude arguments = %#v, want %#v", config.Claude.Arguments, want)
	}
	if !reflect.DeepEqual(config.Common.Mounts, defaults.Common.Mounts) {
		t.Errorf("mounts = %#v, want base mounts", config.Common.Mounts)
	}
	if want := []string{`config.toml overrides common.toml for "common.image"`}; !reflect.DeepEqual(shadowed, want) {
		t.Fatalf("shadowed = %#v, want only the differing key %#v", shadowed, want)
	}
}

func TestLoadLayersReportsEveryDifferingKeySorted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCommonConfig(t, root, `
[common]
docker_storage = "project"
image = "common:image"
no_host_mcp = true
use_host_python_venv = true
keep_container = true

[codex]
arguments = ["--sandbox", "workspace-write"]

[claude]
arguments = ["--model", "opus"]
`)
	writeConfig(t, root, `
[common]
docker_storage = "branch"
image = "local:image"
no_host_mcp = false
use_host_python_venv = false
keep_container = false

[codex]
arguments = []

[claude]
arguments = ["--model", "sonnet"]
`)
	_, shadowed, err := LoadLayers(root, typedDefaults(t))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, key := range []string{
		"claude.arguments", "codex.arguments", "common.docker_storage", "common.image", "common.keep_container",
		"common.no_host_mcp", "common.use_host_python_venv",
	} {
		want = append(want, `config.toml overrides common.toml for "`+key+`"`)
	}
	if !reflect.DeepEqual(shadowed, want) {
		t.Fatalf("shadowed = %#v, want %#v", shadowed, want)
	}
}

func TestLoadLayersAppliesCommonConfigWithoutLocalConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := t.TempDir()
	base := typedDefaults(t)
	base.Common.DependencyCaches = []DependencyCacheConfig{{Kind: DependencyCacheUV, Source: cache}}
	writeCommonConfig(t, root, "[common]\nkeep_container = true\n")
	config, shadowed, err := LoadLayers(root, base)
	if err != nil || len(shadowed) != 0 {
		t.Fatalf("LoadLayers() shadowed = %#v, err = %v", shadowed, err)
	}
	if !config.Common.KeepContainer || !reflect.DeepEqual(config.Common.DependencyCaches, base.Common.DependencyCaches) {
		t.Fatalf("config = %#v, want common value over the generated base", config.Common)
	}
}

func TestLoadLayersRejectsHostSpecificKeysInCommonConfig(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		content string
		key     string
	}{
		{name: "mounts", content: "[common]\nmounts = []\n", key: "common.mounts"},
		{name: "tmpfs", content: "[common]\ntmpfs_mounts = []\n", key: "common.tmpfs_mounts"},
		{name: "caches", content: "[common]\ndependency_caches = []\n", key: "common.dependency_caches"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeCommonConfig(t, root, test.content)
			_, _, err := LoadLayers(root, typedDefaults(t))
			if err == nil || !strings.Contains(err.Error(), test.key) || !strings.Contains(err.Error(), ConfigName) {
				t.Fatalf("LoadLayers() error = %v, want rejection of %s pointing to %s", err, test.key, ConfigName)
			}
		})
	}
}

func TestLoadLayersNamesInvalidCommonConfig(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"[common]\nunknown = true\n", "[common]\ndocker_storage = \"bogus\"\n"} {
		root := t.TempDir()
		writeCommonConfig(t, root, content)
		_, _, err := LoadLayers(root, typedDefaults(t))
		if err == nil || !strings.Contains(err.Error(), CommonConfigName) {
			t.Fatalf("LoadLayers(%q) error = %v, want it to name %s", content, err, CommonConfigName)
		}
	}
}

func TestLoadLayersRejectsSymlinkCommonConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	contextPath := filepath.Join(root, Directory)
	if err := os.Mkdir(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "common.toml")
	if err := os.WriteFile(target, []byte("[common]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(contextPath, CommonConfigName)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadLayers(root, typedDefaults(t)); err == nil {
		t.Fatal("LoadLayers() accepted a symlink common.toml")
	}
}

func writeCommonConfig(t *testing.T, root string, content string) {
	t.Helper()
	writeLayer(t, root, CommonConfigName, content)
}

func writeLayer(t *testing.T, root string, name string, content string) {
	t.Helper()
	contextPath := filepath.Join(root, Directory)
	if err := os.MkdirAll(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextPath, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadConfig resolves both layers over defaults when a test does not inspect the override warnings.
func loadConfig(root string, defaults ProjectConfig) (ProjectConfig, error) {
	config, _, err := LoadLayers(root, defaults)
	return config, err
}
