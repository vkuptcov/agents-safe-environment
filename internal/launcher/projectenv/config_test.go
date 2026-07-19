package projectenv

import (
	"bytes"
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
