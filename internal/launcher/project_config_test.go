package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestDefaultProjectConfigUsesHostAndGitTopology(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	primary := filepath.Join(base, "primary")
	worktree := filepath.Join(base, "worktree")
	commonGit := filepath.Join(primary, ".git")
	for _, path := range []string{home, primary, worktree, commonGit} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitConfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitConfig, []byte("[user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(home, ".codex")
	claudeHome := filepath.Join(home, ".claude")
	claudeConfig := filepath.Join(home, ".claude.json")
	skills := filepath.Join(home, ".agents", "skills")
	for _, path := range []string{codexHome, claudeHome, skills} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(claudeConfig, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := gitproject.Project{
		RequestedDir: worktree,
		WorktreeRoot: worktree,
		PrimaryRoot:  primary,
		CommonGitDir: commonGit,
		Linked:       true,
	}
	config, err := DefaultProjectConfig(project, HostEnvironment{
		HomeDir:          home,
		GitConfig:        gitConfig,
		CodexHome:        codexHome,
		ClaudeConfigDir:  claudeHome,
		ClaudeConfigFile: claudeConfig,
		PersonalSkills:   skills,
	}, "test:image")
	if err != nil {
		t.Fatal(err)
	}
	roles := make(map[projectenv.MountRole]projectenv.MountConfig, len(config.Common.Mounts))
	for _, mount := range config.Common.Mounts {
		roles[mount.Role] = mount
	}
	if mount := roles[projectenv.RolePrimaryCheckout]; mount.ReadOnly != true || mount.Source != primary {
		t.Errorf("primary mount = %#v, want read-only %q", mount, primary)
	}
	if mount := roles[projectenv.RoleCommonGitDir]; mount.Source != commonGit || mount.ReadOnly {
		t.Errorf("common Git mount = %#v, want writable %q", mount, commonGit)
	}
	if mount := roles[projectenv.RoleCodexHome]; mount.Target != filepath.Join(home, ".codex") {
		t.Errorf("Codex mount = %#v, want target below home", mount)
	}
	if mount := roles[projectenv.RoleClaudeHome]; mount.Source != claudeHome || mount.Target != filepath.Join(home, ".claude") {
		t.Errorf("Claude state mount = %#v", mount)
	}
	if mount := roles[projectenv.RoleClaudeConfig]; mount.Source != claudeConfig || mount.Target != filepath.Join(home, ".claude.json") {
		t.Errorf("Claude global config mount = %#v", mount)
	}
	if mount := roles[projectenv.RoleHostMCPChannel]; mount.Source != projectenv.HostMCPChannelSource {
		t.Errorf("host MCP mount = %#v, want logical source", mount)
	}
	if want := []string{"--sandbox", "danger-full-access"}; !reflect.DeepEqual(config.Codex.Arguments, want) {
		t.Errorf("arguments = %#v, want %#v", config.Codex.Arguments, want)
	}
	if want := []string{"--permission-mode", "auto"}; !reflect.DeepEqual(config.Claude.Arguments, want) {
		t.Errorf("Claude arguments = %#v, want %#v", config.Claude.Arguments, want)
	}
	if config.Common.UseHostPythonVenv {
		t.Error("UseHostPythonVenv = true, want safe default false")
	}
	if len(config.Common.TmpfsMounts) != 0 {
		t.Errorf("TmpfsMounts = %#v, want no default entry; discovery masks existing venvs at launch", config.Common.TmpfsMounts)
	}
}

func TestResolveHostEnvironmentUsesOneCanonicalOptionalSnapshot(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	claudeHome := filepath.Join(home, ".claude")
	claudeConfig := filepath.Join(home, ".claude.json")
	skills := filepath.Join(home, ".agents", "skills")
	for _, path := range []string{codexHome, claudeHome, skills} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(claudeConfig, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitConfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitConfig, []byte("[user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := resolveHostEnvironment(HostEnvironmentInputs{
		UserHomeDir: func() (string, error) { return home, nil },
		LookupEnv:   testLookupEnv(nil),
		DiscoverGitConfig: func(gotHome string) (string, error) {
			if gotHome != home {
				t.Errorf("home = %q, want %q", gotHome, home)
			}
			return gitConfig, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if environment.HomeDir != home || environment.GitConfig != gitConfig || environment.CodexHome != codexHome ||
		environment.ClaudeConfigDir != claudeHome || environment.ClaudeConfigFile != claudeConfig ||
		environment.PersonalSkills != skills {
		t.Fatalf("HostEnvironment = %#v, want paths below %q", environment, home)
	}
}

func TestResolveHostEnvironmentUsesExplicitClaudeConfigDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	explicit := filepath.Join(home, "claude-work")
	if err := os.Mkdir(explicit, 0o700); err != nil {
		t.Fatal(err)
	}
	environment, err := resolveHostEnvironment(HostEnvironmentInputs{
		UserHomeDir:       func() (string, error) { return home, nil },
		LookupEnv:         testLookupEnv(map[string]string{"CLAUDE_CONFIG_DIR": explicit}),
		DiscoverGitConfig: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if environment.ClaudeConfigDir != explicit || environment.ClaudeConfigFile != "" {
		t.Fatalf("Claude state = %q, %q", environment.ClaudeConfigDir, environment.ClaudeConfigFile)
	}
}

func TestResolveHostEnvironmentTreatsPartialDefaultClaudeStateAsAbsent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	environment, err := resolveHostEnvironment(HostEnvironmentInputs{
		UserHomeDir:       func() (string, error) { return home, nil },
		LookupEnv:         testLookupEnv(nil),
		DiscoverGitConfig: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if environment.ClaudeConfigDir != "" || environment.ClaudeConfigFile != "" {
		t.Fatalf("partial Claude state must be absent: %#v", environment)
	}
}

func testLookupEnv(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}

func TestResolveHostEnvironmentRejectsIncompleteInputs(t *testing.T) {
	t.Parallel()
	_, err := resolveHostEnvironment(HostEnvironmentInputs{UserHomeDir: func() (string, error) {
		return "", errors.New("unexpected")
	}})
	if err == nil || err.Error() != "host environment inputs are incomplete" {
		t.Fatalf("resolveHostEnvironment() error = %v, want incomplete inputs", err)
	}
}

func TestResolveProjectConfigAppliesOnlyExplicitOverridesAfterTOML(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	projectRoot := filepath.Join(base, "project")
	gitDir := filepath.Join(projectRoot, ".git")
	for _, path := range []string{home, projectRoot, gitDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	project := gitproject.Project{
		RequestedDir: projectRoot, WorktreeRoot: projectRoot, PrimaryRoot: projectRoot, CommonGitDir: gitDir,
	}
	if err := os.Mkdir(filepath.Join(projectRoot, projectenv.Directory), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `[common]
image = "configured:image"
no_host_mcp = true
use_host_python_venv = true
`
	if err := os.WriteFile(filepath.Join(projectRoot, projectenv.Directory, projectenv.ConfigName), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	host := HostEnvironment{HomeDir: home}
	withoutFlags, err := ResolveProjectConfig(project, host, "default:image", launchplan.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if withoutFlags.Config.Common.Image != "configured:image" || !withoutFlags.Options.NoHostMCP ||
		!withoutFlags.Options.UseHostPythonVenv || withoutFlags.Options.ImageOverride {
		t.Fatalf("file resolution = %#v", withoutFlags)
	}

	withFlags, err := ResolveProjectConfig(project, host, "default:image", launchplan.Overrides{
		Image: "flag:image", ImageOverride: true, NoHostMCPOverride: true, UseHostPythonVenvOverride: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if withFlags.Config.Common.Image != "flag:image" || withFlags.Options.NoHostMCP || withFlags.Options.UseHostPythonVenv ||
		!withFlags.Options.ImageOverride {
		t.Fatalf("explicit resolution = %#v", withFlags)
	}
	if data, err := os.ReadFile(filepath.Join(projectRoot, projectenv.Directory, projectenv.ConfigName)); err != nil || string(data) != config {
		t.Fatalf("config file = %q, %v; overrides must not rewrite it", data, err)
	}
	if _, err := ResolveProjectConfig(project, host, "default:image", launchplan.Overrides{
		Image: " invalid:image ", ImageOverride: true,
	}); err == nil {
		t.Fatal("ResolveProjectConfig() accepted an invalid explicit image")
	}
}
