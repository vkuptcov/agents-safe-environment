package launchcli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestResolveConfigSurfacesLayerWarningsWithPersistedConfig(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	projectRoot := filepath.Join(base, "project")
	gitDir := filepath.Join(projectRoot, ".git")
	contextPath := filepath.Join(projectRoot, projectenv.Directory)
	for _, path := range []string{home, gitDir, contextPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	for name, content := range map[string]string{
		projectenv.CommonConfigName: "[common]\nimage = \"common:image\"\n",
		projectenv.ConfigName:       "[common]\nimage = \"local:image\"\n",
	} {
		if err := os.WriteFile(filepath.Join(contextPath, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	project := gitproject.Project{
		Branch:       "main",
		RequestedDir: projectRoot, WorktreeRoot: projectRoot, PrimaryRoot: projectRoot, CommonGitDir: gitDir,
	}

	resolved, err := ResolveConfig(project, "default:image", launchplan.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`config.toml overrides common.toml for "common.image"`}
	if resolved.Image != "local:image" || !reflect.DeepEqual(resolved.Warnings, want) {
		t.Fatalf("ResolveConfig() image = %q, warnings = %#v; want local image and %#v", resolved.Image, resolved.Warnings, want)
	}
}
