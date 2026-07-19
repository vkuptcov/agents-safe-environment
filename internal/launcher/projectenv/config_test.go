package projectenv

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

[codex]
arguments = ["exec", "--model", "gpt-5"]
`)

	config, err := Load(root, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if config.Common.Image != "configured:image" {
		t.Errorf("image = %q, want configured value", config.Common.Image)
	}
	if config.Common.NoHostMCP {
		t.Error("no_host_mcp = true, want explicit false")
	}
	if !reflect.DeepEqual(config.Common.Mounts, defaults.Common.Mounts) {
		t.Errorf("mounts = %#v, want omitted default %#v", config.Common.Mounts, defaults.Common.Mounts)
	}
	if want := []string{"exec", "--model", "gpt-5"}; !reflect.DeepEqual(config.Codex.Arguments, want) {
		t.Errorf("arguments = %#v, want %#v", config.Codex.Arguments, want)
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

	config, err := Load(root, defaults)
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
			name: "malformed channel",
			content: `
[[common.mounts]]
role = "host_mcp_channel"
source = "runtime://unexpected"
target = "/run/codex-safe-host-mcp"
read_only = false
`,
			want: "invalid host MCP channel mount",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, test.content)
			_, err := Load(root, typedDefaults(t))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestEncodeTypedConfigIsDeterministic(t *testing.T) {
	t.Parallel()
	config := typedDefaults(t)
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
}

func typedDefaults(t *testing.T) ProjectConfig {
	t.Helper()
	source := t.TempDir()
	return ProjectConfig{
		Common: CommonConfig{
			Image:     "default:image",
			NoHostMCP: true,
			Mounts: []MountConfig{{
				Role:   RoleAdditional,
				Source: source,
				Target: source,
			}},
		},
		Codex: CodexConfig{Arguments: []string{"--sandbox", "danger-full-access"}},
	}
}

func TestLoadMounts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := t.TempDir()
	secondTarget := t.TempDir()
	secondLink := filepath.Join(t.TempDir(), "mount-link")
	if err := os.Symlink(secondTarget, secondLink); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, fmt.Sprintf("mounts = [%q, %q, %q]\n", first, secondLink, first))

	mounts, err := LoadMounts(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{first, secondTarget}
	if !reflect.DeepEqual(mounts, want) {
		t.Fatalf("LoadMounts() = %#v, want %#v", mounts, want)
	}
}

func TestLoadMountsAbsentConfig(t *testing.T) {
	t.Parallel()
	for _, root := range []string{t.TempDir(), writeContext(t)} {
		mounts, err := LoadMounts(root)
		if err != nil || mounts != nil {
			t.Fatalf("LoadMounts(%q) = (%#v, %v), want nil, nil", root, mounts, err)
		}
	}
}

func TestLoadMountsRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "malformed", content: "mounts = [", want: "parse project environment config"},
		{name: "unknown", content: "unknown = true\n", want: "unknown setting"},
		{name: "relative", content: "mounts = [\"relative\"]\n", want: "literal absolute path"},
		{name: "root", content: "mounts = [\"/\"]\n", want: "filesystem root"},
		{name: "missing", content: "mounts = [\"/path/that/does/not/exist\"]\n", want: "resolve configured mount"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, test.content)
			_, err := LoadMounts(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadMounts() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadMountsRejectsFileAndSymlinkConfig(t *testing.T) {
	t.Parallel()
	t.Run("mount source file", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		writeConfig(t, root, fmt.Sprintf("mounts = [%q]\n", file))
		_, err := LoadMounts(root)
		if err == nil || !strings.Contains(err.Error(), "is not a directory") {
			t.Fatalf("LoadMounts() error = %v, want directory rejection", err)
		}
	})
	t.Run("config symlink", func(t *testing.T) {
		root := t.TempDir()
		contextPath := filepath.Join(root, Directory)
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(target, []byte("mounts = []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(contextPath, ConfigName)); err != nil {
			t.Fatal(err)
		}
		_, err := LoadMounts(root)
		if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
			t.Fatalf("LoadMounts() error = %v, want symlink rejection", err)
		}
	})
}

func writeConfig(t *testing.T, root string, content string) {
	t.Helper()
	contextPath := filepath.Join(root, Directory)
	if err := os.MkdirAll(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextPath, ConfigName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
